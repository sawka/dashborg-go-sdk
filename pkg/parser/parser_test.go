package parser

import (
	"testing"
)

func reportErr(t *testing.T, pc *ParseContext, errStr string) {
	t.Errorf("%s, len=%d, pos=%d, errs=%v | << %s >>", errStr, len(pc.Line), pc.Pos, pc.Errs, pc.Line)
}

func reportErrElem(t *testing.T, pc *ParseContext, e *ElemDecl, errStr string) {
	t.Errorf("%s, len=%d, pos=%d, errs=%v | elem[%#v] | << %s >>", errStr, len(pc.Line), pc.Pos, pc.Errs, e, pc.Line)
}

func TestBasic(t *testing.T) {
	pc := MakeParseContext("  ", 1, nil)
	pc.ws()
	if !pc.iseof() {
		t.Errorf("error parsing ws")
	}

	pc = MakeParseContext("#  hello again!", 1, nil)
	pc.comment()
	if !pc.iseof() {
		t.Errorf("error parsing comment 1 len=%d, pos=%d", len(pc.Line), pc.Pos)
	}

	vars := make(map[string]string)
	pc = MakeParseContext("$foo = bar", 1, vars)
	pc.vardecl()
	if pc.Vars["foo"] != "bar" {
		reportErr(t, pc, "error parsing vardecl 1")
	}

	pc = MakeParseContext("hello", 1, vars)
	e := pc.itext()
	if e == nil {
		reportErr(t, pc, "itext1 no elem returned")
	}
	if e != nil && e.ElemType != "text" {
		reportErr(t, pc, "itext1 did not parse elemtype text")
	}
	if e != nil && e.Text != "hello" {
		reportErrElem(t, pc, e, "itext1 wrong text in elem")
	}

	pc = MakeParseContext("[]", 1, nil)
	_, _, ok := pc.attrs()
	if !ok {
		reportErr(t, pc, "attrs0 not ok")
	}

	pc = MakeParseContext("[ @h1 @foo=bar @maxHeight=5px]", 1, nil)
	_, attrs, ok := pc.attrs()
	if !ok {
		reportErr(t, pc, "attrs1 not ok")
	}
	if attrs["foo"] != "bar" {
		reportErr(t, pc, "attrs1 foo != bar")
	}
	if attrs["h1"] != "1" {
		reportErr(t, pc, "attrs1 h1 not set")
	}
	if attrs["maxheight"] != "5px" {
		reportErr(t, pc, "attrs1 attrs not lowercased")
	}

	pc = MakeParseContext("[rootdiv @foo=bar]", 1, nil)
	cns, attrs, ok := pc.attrs()
	if !ok {
		reportErr(t, pc, "attrs2 not ok")
	}
	if attrs["foo"] != "bar" {
		reportErr(t, pc, "attrs2 foo != bar")
	}
	if len(cns) != 1 || cns[0] != "rootdiv" {
		reportErr(t, pc, "attrs2 bad classnames parse")
	}

	pc = MakeParseContext("[rootdiv:dark @foo=bar]", 1, nil)
	cns, attrs, ok = pc.attrs()
	if !ok {
		reportErr(t, pc, "attrs3 not ok")
	}
	if attrs["foo"] != "bar" {
		reportErr(t, pc, "attrs3 foo != bar")
	}
	if len(cns) != 2 || cns[0] != "rootdiv" || cns[1] != "dark" {
		reportErr(t, pc, "attrs3 bad classnames parse")
	}

	pc = MakeParseContext("[@foo=bar]  hello", 1, vars)
	e = pc.itext()
	if e == nil {
		reportErr(t, pc, "itext2 no elem returned")
	}
	if e != nil && e.ElemType != "text" {
		reportErr(t, pc, "itext2 did not parse elemtype text")
	}
	if e != nil && e.Text != "hello" {
		reportErr(t, pc, "itext2 wrong text in elem")
	}
	if e != nil && e.Attrs["foo"] != "bar" {
		reportErr(t, pc, "itext2 bad attr parse")
	}

	pc = MakeParseContext("</div>", 1, vars)
	e = pc.elem()
	if e == nil {
		reportErr(t, pc, "closeelem1 no elem returned")
	}
	if e != nil && (e.ElemType != "div" || !e.IsClose) {
		reportErrElem(t, pc, e, "closeelem1 not parsed correctly")
	}

	pc = MakeParseContext("</div:s1>", 1, vars)
	e = pc.elem()
	if e == nil {
		reportErr(t, pc, "closeelem2 no elem returned")
	}
	if e != nil && (e.ElemType != "div" || e.ElemSubType != "s1" || !e.IsClose) {
		reportErrElem(t, pc, e, "closeelem2 not parsed correctly")
	}

	pc = MakeParseContext("<div>", 1, vars)
	e = pc.elem()
	if e == nil {
		reportErr(t, pc, "openelem1 no elem returned")
	}
	if e != nil && (e.ElemType != "div") {
		reportErrElem(t, pc, e, "openelem1 not parsed correctly")
	}

	pc = MakeParseContext("<div:foo/>", 1, vars)
	e = pc.elem()
	if e == nil {
		reportErr(t, pc, "openelem2 no elem returned")
	}
	if e != nil && (e.ElemType != "div" || e.ElemSubType != "foo" || !e.IsSelfClose) {
		reportErrElem(t, pc, e, "openelem2 not parsed correctly")
	}

	pc = MakeParseContext("<div> [@h1 @height=20px]", 1, vars)
	e = pc.elem()
	if e == nil {
		reportErr(t, pc, "openelem3 no elem returned")
	}
	if e != nil && (e.ElemType != "div") {
		reportErrElem(t, pc, e, "openelem3 not parsed correctly")
	}
	if e != nil && (e.Attrs["height"] != "20px" || e.Attrs["h1"] != "1") {
		reportErrElem(t, pc, e, "openelem3 bad attrs")
	}

	pc = MakeParseContext("<button/> [@primary @height=30px] Click!", 1, vars)
	e = pc.elem()
	if e == nil {
		reportErr(t, pc, "openelem4 no elem returned")
	}
	if e != nil && (e.ElemType != "button" || !e.IsSelfClose) {
		reportErrElem(t, pc, e, "openelem4 not parsed correctly")
	}
	if e != nil && (e.Attrs["height"] != "30px" || e.Attrs["primary"] != "1") {
		reportErrElem(t, pc, e, "openelem4 bad attrs")
	}
	if e != nil {
		if e.SubElem == nil {
			reportErrElem(t, pc, e, "openelem4 no subelem")
		} else {
			if e.SubElem.ElemType != "text" || e.SubElem.Text != "Click!" {
				reportErrElem(t, pc, e.SubElem, "openelem4 bad subelem")
			}
		}
	}

	pc = MakeParseContext("<div> [@border='1px solid black' @foo=\"dq test \\\"ab\\\\c\\\"\"]", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line1 no elem returned")
	}
	if e != nil && (e.ElemType != "div") {
		reportErrElem(t, pc, e, "line1 wrong elemtype")
	}
	if e != nil && (e.Attrs["border"] != "1px solid black") {
		reportErrElem(t, pc, e, "line1 bad singlequote attr parse")
	}
	if e != nil && (e.Attrs["foo"] != "dq test \"ab\\c\"") {
		reportErrElem(t, pc, e, "line1 bad doublequote attr parse")
	}

	pc = MakeParseContext("<div *cd2a95fb-40e5-48bc-b475-f38ec7a5263d>", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line2 no elem returned")
	}
	if e != nil && e.ControlId != "cd2a95fb-40e5-48bc-b475-f38ec7a5263d" {
		reportErrElem(t, pc, e, "line2 controlid not parsed")
	}

	pc = MakeParseContext("<div \"p-1\">", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line3 no elem returned")
	}
	if e != nil && e.ControlName != "p-1" {
		reportErrElem(t, pc, e, "line3 quoted controlname not parsed")
	}

	pc = MakeParseContext("<handler /acc-test>", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line4 no elem returned")
	}
	if e != nil && e.ControlName != "/acc-test" {
		reportErrElem(t, pc, e, "line4 quoted controlname not parsed")
	}

	pc = MakeParseContext("<handler /acc-test/>", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line5 no elem returned")
	}
	if e != nil && e.ControlName != "/acc-test" {
		reportErrElem(t, pc, e, "line5 quoted controlname not parsed")
	}
	if e != nil && !e.IsSelfClose {
		reportErrElem(t, pc, e, "line5 not self close")
	}

	pc = MakeParseContext("<div>[rootdiv]", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line6 no elem returned")
	}
	if e != nil && (len(e.ClassNames) != 1 || e.ClassNames[0] != "rootdiv") {
		reportErrElem(t, pc, e, "line6 bad classnames parse")
	}

	pc = MakeParseContext("<button b-1>[primary] Run Process #1", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line7 no elem returned")
	}
	if e != nil && e.ControlName != "b-1" {
		reportErr(t, pc, "line7 bad controlname")
	}
	if e != nil && (e.SubElem == nil || e.SubElem.ElemType != "text") {
		reportErr(t, pc, "line7 bad subelem")
	}

	pc = MakeParseContext("<log demo-logger/>[dark @grow] ", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line8 no elem returned")
	}
	if e.SubElem != nil {
		reportErr(t, pc, "line8 should not have subelem")
	}

	pc = MakeParseContext("<log *10300ca2-2325-4315-90ad-b8f0988dde4b \"demo\"/>[dark @grow] ", 1, vars)
	e = pc.ParseLine()
	if e == nil {
		reportErr(t, pc, "line9 no elem returned")
	}
	if e != nil && e.ControlId != "10300ca2-2325-4315-90ad-b8f0988dde4b" {
		reportErrElem(t, pc, e, "line9 did not parse controlid")
	}
	if e != nil && e.ControlName != "demo" {
		reportErrElem(t, pc, e, "line9 did not parse controlname")
	}
}
