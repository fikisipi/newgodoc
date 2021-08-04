package main

// This comment is under.

import (
	"go/ast"
	"os"
	"strings"
	"path/filepath"
	"github.com/alecthomas/kong"
	"net/http"
	"golang.org/x/tools/godoc"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/godoc/vfs"
	"time"
	"io/fs"
	"reflect"
)

var Cli struct {
	Out    string `default:"dist" short:"o" help"Where to put documentation/assets."`
	Module string `arg help:"Path to module/package for documentation generation."`
	StartServer bool `help:"Run a server once docs are built."`
	Open   bool
}

func cliParse() {
	kong.Parse(&Cli)
	cliOutputAbs, err := filepath.Abs(Cli.Out)
	if err != nil {
		fmt.Red("Couldn't parse directory for output", err)
		os.Exit(1)
	}
	Cli.Out = cliOutputAbs
	if cliStat, err := os.Stat(Cli.Out); err == nil {
		if !cliStat.IsDir() {
			fmt.Red("Output is not a directory, but a file.")
			os.Exit(1)
		}
		isFine := true
		filepath.WalkDir(Cli.Out, func(path string, d fs.DirEntry, err error) error {
			if !d.IsDir() && filepath.Ext(path) != ".html" {
				fmt.Red("Out path not empty (contains non-assets):", Cli.Out)
				os.Exit(1)
				isFine = false
				return filepath.SkipDir
			}
			return nil
		})
		if !isFine {
			return
		}
	}

	fmt.Yellow("Using \"" + Cli.Out + "\" as an output directory...")

	absModPath, err := filepath.Abs(Cli.Module)
	mInfo, err := os.Stat(absModPath)
	if err != nil {
		fmt.Red("Error loading '", mInfo, "': ", err)
		os.Exit(1)
	}
	mDirPath := absModPath
	if !mInfo.IsDir() {
		mDirPath = filepath.Dir(Cli.Module)
	}

	_modDoc = ModuleParse(mDirPath)
}

var _modDoc *ModuleDoc = nil

func ModuleParse(modFilePath string) (parsedModuleDoc *ModuleDoc) {
	parsedModuleDoc = new(ModuleDoc)
	parsedModuleDoc.Packages = []*PackageDoc{}
	parsedModuleDoc.SimpleExports = SimpleExportsByType{}

	fmt.Debug("modFilePath", modFilePath)
	c := godoc.NewCorpus(vfs.OS(modFilePath))

	err := c.Init()
	if err != nil {
		fmt.Red(err)
	}
	go func() {
		c.RunIndexer()
	}()
	<-time.NewTicker(time.Millisecond * 200).C

	idx, _ := c.CurrentIndex()

	goModBuffer, err := os.ReadFile(filepath.Join(modFilePath, "go.mod"))
	modImportPath := modfile.ModulePath(goModBuffer)

	parsedModuleDoc.AbsolutePath = modFilePath
	parsedModuleDoc.ImportPath = modImportPath

	pkgList := map[string]string{}
	for kind, symbols := range idx.Idents() {
		if kind.Name() == "Packages" {
			for _, sym := range symbols {
				pkgList[sym[0].Path] = sym[0].Name
			}
		} else {
			for name, symTable := range symbols {
				for _, symbol := range symTable {
					scopedId := ScopedIdentifier{
						PackagePath: symbol.Path,
						Name:        name,
						IsFunction:  kind == godoc.FuncDecl,
						IsMethod:    kind == godoc.MethodDecl,
						isType:      kind == godoc.TypeDecl,
					}
					parsedModuleDoc.SimpleExports[name] = append(parsedModuleDoc.SimpleExports[name], scopedId)
				}
			}
		}
	}
	parsedModuleDoc.DebugPrint()
	fmt.Green("Loaded packages:", pkgList)

	godocPresentation := godoc.NewPresentation(c)
	for path, pkgName := range pkgList {
		parsedPackage := new(PackageDoc)
		info := godocPresentation.GetPkgPageInfo(path, pkgName, godoc.NoFiltering)
		if info == nil {
			continue
		}

		parsedPackage.FileDecls = make(map[string][]BaseDef)
		parsedPackage.ParentModule = parsedModuleDoc
		parsedPackage.AbsolutePath = filepath.Join(modFilePath, strings.TrimPrefix(path, "/"))
		parsedPackage.FileSet = info.FSet
		parsedPackage.RelativePath = path
		parsedPackage.Name = pkgName
		parsedPackage.Doc = info.PDoc.Doc

		parsedModuleDoc.Packages = append(parsedModuleDoc.Packages, parsedPackage)

		for _, tp := range info.PDoc.Types {
			for _, spec := range tp.Decl.Specs {
				ParseTypeDecl(spec, parsedPackage)
			}
		}

		for _, fn := range info.PDoc.Funcs {
			parsedFn := FunctionDef{}

			parsedFn.Snippet = CreateSnippet(fn.Decl, parsedPackage)
			parsedFn.Name = fn.Name
			parsedFn.Doc = fn.Doc
			parsedPackage.Functions = append(parsedPackage.Functions, parsedFn)
			parsedFn.FoundInFile = GetDeclFile(fn.Decl, parsedFn.BaseDef, parsedPackage)
		}

		for _, varVal := range info.PDoc.Vars {
			for _, varName := range varVal.Names {
				fmt.Debug(varName)
			}
			fmt.Debug("specs", varVal.Decl.Specs)
			_ = varVal
		}

		for _, constVal := range info.PDoc.Consts {
			for _, constName := range constVal.Names {
				fmt.Debug(constName)
			}
			fmt.Debug(reflect.TypeOf(constVal.Decl.Specs[0]))
			_ = constVal
		}

		//fmt.Println(info.CallGraphIndex)
		for file, decls := range parsedPackage.FileDecls {
			fmt.Debug(file, decls)
		}
	}

	return
}

func ParseTypeDecl(s ast.Spec, docPackage *PackageDoc) {
	t := s.(*ast.TypeSpec)
	declName := t.Name.Name
	st, ok := t.Type.(*ast.StructType)
	if ok {
		sDef := StructDef{}
		sDef.Snippet = CreateSnippet(st, docPackage, "type ", declName, " ")
		sDef.Name = declName
		sDef.Type = st
		sDef.FoundInFile = GetDeclFile(st, sDef.BaseDef, docPackage)

		for _, field := range st.Fields.List {
			_ = field
		}
		docPackage.Structs = append(docPackage.Structs, sDef)
	} else {
		it, ok := t.Type.(*ast.InterfaceType)
		if !ok {
			return
		}
		interDef := InterfaceDef{}
		interDef.FoundInFile = GetDeclFile(it, interDef.BaseDef, docPackage)
		interDef.Name = declName
		interDef.Type = it
		interDef.Snippet = CreateSnippet(it, docPackage, "type ", declName, " ")
		docPackage.Interfaces = append(docPackage.Interfaces, interDef)

		for _, meth := range it.Methods.List {
			_ = meth
		}
	}
}

var ModulePath string

func main() {
	cliParse()

	GenerateHTML(_modDoc)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		GenerateHTML(_modDoc)

		http.FileServer(http.Dir(Cli.Out)).ServeHTTP(writer, request)
	})

	if Cli.StartServer {
		err := http.ListenAndServe(":8080", mux)
		if err != nil {
			fmt.Red("Cannot listen on :8080 ", err)
			os.Exit(1)
		}
		fmt.Green("Listening on :8080")
	}
}
