package parser

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// <[type]:[subtype?] /? >[[class:class?] attrs...] text

// Elem Grammar
//
// line := ws (comment | vardecl | itext | elem)?
// comment := '#' rest
// ws := whitespace-char*
// uuid := [a-f0-9][a-f0-9-]{35}
// rest := all characters to end of line
// vardecl := '$' varname ws '=' ws rest
// varname := [A-Za-z][A-Za-z0-9_]*
// itext := attrs? (text | varexpr | charexpr)*
// text := all chars except '${' and '$('
// varexpr := '${' varname '}'
// elem := openelem | closeelem
// closeelem := '</' varname (':' varname)? ws '>'
// openelem := '<' varname (':' varname)? ws (('*' uuid) | controlname_quoted | controlname)? ws '/'? '>' ws attrs ws itext?
// controlname_quoted := '"' [a-zA-Z0-9_.:#/-]+ '"'
// controlname := [a-zA-Z0-9_.:#/-]+ (will not match a trailing '/' if followed immediately by '>')
// attrs := '[' (varname (':' varname)*)? (ws attr)* ws ']'
// attr := '@' varname ('=' attrexpr)?
// attrexpr := attr_noquote | attr_singlequote | attr_doublequote
// attr_noquote := [^ \]]+
// attr_singlequote := '\'' [^']+ '\''
// attr_doublequote := '"' (dqtext | varexpr | '\\"')* '"'
// dqtext := all chars except '${' and '"' and '$('
// charexpr := '$(' ws varname ws ',' ws (attr_doublequote | attr_singlequote) ws ')'
//
// <progress "p-1">

type ElemDecl struct {
	ElemType    string
	ElemSubType string
	ControlName string
	ControlId   string
	IsSelfClose bool
	IsClose     bool
	ClassNames  []string
	Attrs       map[string]string
	Text        string
	SubElem     *ElemDecl
}

type ParseErr struct {
	LineNo int
	Pos    int
	Err    string
}

type ParseContext struct {
	Line     []byte
	LineNo   int
	Pos      int
	Vars     map[string]string
	Errs     []ParseErr
	Warnings []ParseErr
}

func MakeParseContext(line string, lineno int, vars map[string]string) *ParseContext {
	return &ParseContext{
		Line:   []byte(line),
		LineNo: lineno,
		Pos:    0,
		Vars:   vars,
	}
}

func (ctx *ParseContext) addErr(fmtStr string, params ...interface{}) {
	err := fmt.Sprintf(fmtStr, params...)
	ctx.Errs = append(ctx.Errs, ParseErr{LineNo: ctx.LineNo, Pos: ctx.Pos, Err: err})
}

func (ctx *ParseContext) iseof() bool {
	return ctx.Pos == len(ctx.Line)
}

func (ctx *ParseContext) peek() rune {
	r, _ := utf8.DecodeRune(ctx.Line[ctx.Pos:])
	return r
}

func (ctx *ParseContext) peek2() rune {
	_, size := utf8.DecodeRune(ctx.Line[ctx.Pos:])
	r2, _ := utf8.DecodeRune(ctx.Line[ctx.Pos+size:])
	return r2
}

func (ctx *ParseContext) test(r rune) bool {
	return ctx.peek() == r
}

func (ctx *ParseContext) test2(r1 rune, r2 rune) bool {
	return ctx.peek() == r1 && ctx.peek2() == r2
}

func (ctx *ParseContext) match(r rune) bool {
	if ctx.peek() == r {
		ctx.advance()
		return true
	}
	return false
}

func (ctx *ParseContext) match2(r1 rune, r2 rune) bool {
	if ctx.peek() == r1 && ctx.peek2() == r2 {
		ctx.advance()
		ctx.advance()
		return true
	}
	return false
}

func (ctx *ParseContext) isSimpleAlpha() bool {
	ch := ctx.peek()
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func (ctx *ParseContext) isVarDeclChar() bool {
	ch := ctx.peek()
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

func (ctx *ParseContext) advance() rune {
	r, size := utf8.DecodeRune(ctx.Line[ctx.Pos:])
	ctx.Pos += size
	return r
}

func (ctx *ParseContext) ws() {
	for unicode.IsSpace(ctx.peek()) {
		ctx.advance()
	}
}

func (ctx *ParseContext) uuid() string {
	var buf bytes.Buffer
	for i := 0; i < 36; i++ {
		ch := ctx.peek()
		if !(ch >= 'a' && ch <= 'f' || ch >= '0' && ch <= '9' || ch == '-') || (ch == '-' && i == 0) {
			ctx.addErr("bad uuid character")
			return ""
		}
		ctx.advance()
		buf.WriteRune(ch)
	}
	return buf.String()
}

func (ctx *ParseContext) ParseLine() *ElemDecl {
	ctx.ws()
	n := ctx.peek()
	switch n {
	case '#':
		ctx.comment()

	case '$':
		ctx.vardecl()

	case '[':
		return ctx.itext()

	case '<':
		return ctx.elem()

	default:
		return ctx.itext()
	}
	return nil
}

func (ctx *ParseContext) comment() {
	if !ctx.match('#') {
		ctx.addErr("comment does not start with '#'")
		return
	}
	ctx.Pos = len(ctx.Line)
	return
}

func (ctx *ParseContext) varname() string {
	mark := ctx.Pos
	if !ctx.isSimpleAlpha() {
		ctx.addErr("varname must start with a-zA-Z")
		return ""
	}
	for {
		if !ctx.isVarDeclChar() {
			break
		}
		ctx.advance()
	}
	return string(ctx.Line[mark:ctx.Pos])
}

func (ctx *ParseContext) vardecl() {
	if !ctx.match('$') {
		ctx.addErr("vardecl does not start with '$'")
		return
	}
	name := ctx.varname()
	if name == "" {
		ctx.addErr("invalid varname in vardecl")
		return
	}
	ctx.ws()
	if !ctx.match('=') {
		ctx.addErr("invalid varname, or no '=' in vardecl")
		return
	}
	ctx.ws()
	val := string(ctx.Line[ctx.Pos:])
	ctx.Pos = len(ctx.Line)
	ctx.Vars[name] = val
}

func (ctx *ParseContext) attr_noquote() (string, bool) {
	mark := ctx.Pos
	for {
		if ctx.iseof() {
			ctx.addErr("unterminated attribute value")
			return "", false
		}
		ch := ctx.peek()
		if ch == ']' || unicode.IsSpace(ch) {
			break
		}
		ctx.advance()
	}
	return string(ctx.Line[mark:ctx.Pos]), true
}

func (ctx *ParseContext) attr_doublequote() (string, bool) {
	if !ctx.match('"') {
		ctx.addErr("attr doublequote must start with \" char")
		return "", false
	}
	var buf bytes.Buffer
	for {
		if ctx.iseof() {
			ctx.addErr("unterminated double quoted attribute value")
			return "", false
		}
		if ctx.test('"') {
			break
		}
		if ctx.match('\\') {
			if ctx.iseof() {
				ctx.addErr("unterminated backslash escape / double quoted string, end of line")
				return "", false
			}
			ch := ctx.advance()
			buf.WriteRune(ch)
			continue
		}
		if ctx.test2('$', '{') {
			ctx.addErr("dq var interpolation not supported")
			return "", false
		}
		ch := ctx.advance()
		buf.WriteRune(ch)
	}
	ctx.match('"')
	return buf.String(), true
}

func (ctx *ParseContext) attr_singlequote() (string, bool) {
	if !ctx.match('\'') {
		ctx.addErr("attr singlequote must start with ' char")
		return "", false
	}
	mark := ctx.Pos
	for {
		if ctx.iseof() {
			ctx.addErr("unterminated single quoted attribute value")
			return "", false
		}
		if ctx.test('\'') {
			break
		}
		ctx.advance()
	}
	rtn := string(ctx.Line[mark:ctx.Pos])
	ctx.match('\'')
	return rtn, true
}

func (ctx *ParseContext) attr() (string, string, bool) {
	attrName := ctx.varname()
	if attrName == "" {
		ctx.addErr("invalid attribute name")
		return "", "", false
	}
	ch := ctx.peek()
	if unicode.IsSpace(ch) {
		return attrName, "1", true
	}
	if ch != '=' {
		ctx.addErr("bad attribute declaration, bad attribute name or no '=' found")
		return "", "", false
	}
	ctx.advance()
	var attrVal string
	var ok bool
	ch = ctx.peek()
	if ch == '"' {
		attrVal, ok = ctx.attr_doublequote()
	} else if ch == '\'' {
		attrVal, ok = ctx.attr_singlequote()
	} else {
		attrVal, ok = ctx.attr_noquote()
	}
	if !ok {
		return "", "", false
	}
	return attrName, attrVal, true
}

func (ctx *ParseContext) attrs() (map[string]string, bool) {
	if !ctx.match('[') {
		ctx.addErr("bad attribute, no opening '['")
		return nil, false
	}
	rtn := make(map[string]string)
	// classnames
	if ctx.isSimpleAlpha() {

	}
	// attrs
	for {
		ctx.ws()
		if ctx.iseof() {
			ctx.addErr("unterminated attributes block")
			return nil, false
		}
		ch := ctx.peek()
		if ch == ']' {
			break
		}
		if !ctx.match('@') {
			ctx.addErr("attribute names must start with '@'")
			return nil, false
		}
		attrName, attrVal, ok := ctx.attr()
		if !ok {
			return nil, false
		}
		rtn[strings.ToLower(attrName)] = attrVal
	}
	if !ctx.match(']') {
		ctx.addErr("attributes not terminated correct with closing ']'")
		return nil, false
	}
	return rtn, true
}

func (ctx *ParseContext) itext() *ElemDecl {
	rtn := &ElemDecl{ElemType: "text"}
	if ctx.test('[') {
		var ok bool
		rtn.Attrs, ok = ctx.attrs()
		if !ok {
			return nil
		}
		ctx.ws()
	}
	var buf bytes.Buffer
	for {
		if ctx.iseof() {
			break
		}
		if ctx.test2('$', '{') {
			ctx.addErr("itext var interpolation not supported")
			ctx.advance()
			ctx.advance()
			buf.WriteRune('$')
			buf.WriteRune('{')
			continue
		}
		ch := ctx.advance()
		buf.WriteRune(ch)
	}
	rtn.Text = buf.String()
	return rtn
}

func (ctx *ParseContext) elemtypeandsubtype() (string, string, bool) {
	var elemType string
	var elemSubType string

	elemType = ctx.varname()
	if elemType == "" {
		return "", "", false
	}
	if ctx.test(':') {
		ctx.advance()
		elemSubType = ctx.varname()
		if elemSubType == "" {
			return "", "", false
		}
	}
	return elemType, elemSubType, true
}

func (ctx *ParseContext) closeelem() *ElemDecl {
	if !ctx.match2('<', '/') {
		ctx.addErr("bad closeelem, does not start with '</'")
		return nil
	}
	elemType, elemSubType, ok := ctx.elemtypeandsubtype()
	if !ok {
		return nil
	}
	ctx.ws()
	if !ctx.match('>') {
		ctx.addErr("bad closeelem, not terminated correctly with '>'")
		return nil
	}
	ctx.ws()
	if !ctx.iseof() {
		ctx.addErr("bad closeelem, extraneous text after closing '>'")
		return nil
	}
	return &ElemDecl{ElemType: elemType, ElemSubType: elemSubType, IsClose: true}
}

const CONTROLNAMECHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:#/-"

func (ctx *ParseContext) controlname_quoted() string {
	if !ctx.match('"') {
		ctx.addErr("controlname must be quoted")
		return ""
	}
	var buf bytes.Buffer
	for {
		if ctx.iseof() {
			ctx.addErr("controlname not terminated correctly")
			return ""
		}
		if ctx.test('"') {
			break
		}
		ch := ctx.peek()
		if ch > unicode.MaxASCII || strings.IndexByte(CONTROLNAMECHARS, byte(ch)) == -1 {
			ctx.addErr("invalid controlname character")
			return ""
		}
		ctx.advance()
		buf.WriteRune(ch)
	}
	if !ctx.match('"') {
		ctx.addErr("controlname not terminated correctly")
		return ""
	}
	return buf.String()
}

func (ctx *ParseContext) controlname() string {
	var buf bytes.Buffer
	for {
		if ctx.iseof() {
			ctx.addErr("controlname not terminated correctly")
			return ""
		}
		ch := ctx.peek()
		if ch > unicode.MaxASCII || strings.IndexByte(CONTROLNAMECHARS, byte(ch)) == -1 {
			break
		}
		if ctx.test2('/', '>') {
			break
		}
		ctx.advance()
		buf.WriteRune(ch)
	}
	return buf.String()
}

func (ctx *ParseContext) openelem() *ElemDecl {
	if !ctx.match('<') {
		ctx.addErr("bad openelem, does not start with '<'")
		return nil
	}
	elemType, elemSubType, ok := ctx.elemtypeandsubtype()
	if !ok {
		return nil
	}
	rtn := &ElemDecl{ElemType: elemType, ElemSubType: elemSubType}
	ctx.ws()
	if ctx.match('*') {
		controlId := ctx.uuid()
		if controlId == "" {
			return nil
		}
		rtn.ControlId = controlId
	} else if ctx.test('"') {
		controlName := ctx.controlname_quoted()
		if controlName == "" {
			return nil
		}
		rtn.ControlName = controlName
	} else if ch := ctx.peek(); ch <= unicode.MaxASCII && strings.IndexByte(CONTROLNAMECHARS, byte(ch)) != -1 {
		if !ctx.test2('/', '>') {
			controlName := ctx.controlname()
			if controlName == "" {
				return nil
			}
			rtn.ControlName = controlName
		}
	}
	ctx.ws()
	if ctx.match('/') {
		rtn.IsSelfClose = true
	}
	if !ctx.match('>') {
		ctx.addErr("bad openelem, not terminated correctly with '>'")
		return nil
	}
	ctx.ws()
	if ctx.test('[') {
		var ok bool
		rtn.Attrs, ok = ctx.attrs()
		if !ok {
			return nil
		}
		ctx.ws()
	}
	if ctx.iseof() {
		return rtn
	}
	subElem := ctx.itext()
	if subElem != nil {
		ctx.ws()
	}
	if len(ctx.Errs) > 0 {
		return nil
	}
	rtn.SubElem = subElem
	return rtn
}

func (ctx *ParseContext) elem() *ElemDecl {
	if ctx.test2('<', '/') {
		return ctx.closeelem()
	}
	if ctx.test('<') {
		return ctx.openelem()
	}
	ctx.addErr("cannot parse elem, must start with '<'")
	return nil
}
