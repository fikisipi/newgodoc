package main

import (
	"github.com/fatih/color"
	"log"
	"os"
)

type _mPrintlnType func(...interface{})
func _mWrapColor(c color.Attribute) _mPrintlnType { return func(x ...interface{}) { color.New(c).Println(x...) } }
var cliPrint = struct {
	Red   _mPrintlnType
	Green _mPrintlnType
	Debug _mPrintlnType
}{
	_mWrapColor(color.FgRed), _mWrapColor(color.FgGreen), log.New(os.Stdout, "DBG ", log.Flags()).Println,
}