package parser

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

// <[type]:[subtype?] /? >[[class:class?] attrs...] text

// Elem Grammar
//
// line := ws (comment | vardecl | itext | elem | mtext)?
// comment := '#' rest
// ws := whitespace-char*
// uuid := [a-f0-9][a-f0-9-]{35}
// rest := all characters to end of line
// vardecl := '$' varname ws '=' ws rest
// varname := [A-Za-z][A-Za-z0-9_]*
// itext := attrs? (text | varexpr | charexpr)*
// text := all chars except '${' and '$(' and '||' ('||' is allowed for itext, but not mtext)
// mtext := '*' attrs itext ('||' itext)*   (must start with "*[")
// varexpr := '${' varname (':' formatexpr)? '}'
// formatexpr := '%' [^}]+
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
	List        []*ElemDecl

	VarName  string
	VarValue string
}

type ParseErr struct {
	LineNo int
	Col    int
	Err    string
}

func (e *ParseErr) String() string {
	if e.LineNo == 0 {
		return fmt.Sprintf("ERROR pos:%d %s", e.Col, e.Err)
	}
	return fmt.Sprintf("ERROR line:%d pos:%d %s", e.LineNo, e.Col, e.Err)
}

type VarLookupFn func(string) interface{}

func MapVarFn(m map[string]interface{}) VarLookupFn {
	return func(name string) interface{} {
		return m[name]
	}
}

func Map2VarFn(m1 map[string]interface{}, m2 map[string]interface{}) VarLookupFn {
	return func(name string) interface{} {
		v1, ok := m1[name]
		if ok {
			return v1
		}
		return m2[name]
	}
}

type ParseContext struct {
	Line   []byte
	LineNo int
	Pos    int
	VarFn  VarLookupFn
	Errs   []ParseErr
	Warns  []ParseErr
}

func MakeParseContext(line string, lineno int, varFn VarLookupFn) *ParseContext {
	return &ParseContext{
		Line:   []byte(line),
		LineNo: lineno,
		Pos:    0,
		VarFn:  varFn,
	}
}

func (ctx *ParseContext) addErr(fmtStr string, params ...interface{}) {
	err := fmt.Sprintf(fmtStr, params...)
	ctx.Errs = append(ctx.Errs, ParseErr{LineNo: ctx.LineNo, Col: ctx.Pos + 1, Err: err})
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
		return ctx.vardecl()

	case '[':
		decl, _ := ctx.itext(false)
		return decl

	case '<':
		return ctx.elem()

	case '*':
		if ctx.peek2() == '[' {
			decl, _ := ctx.mtext()
			return decl
		} else {
			decl, _ := ctx.itext(false)
			return decl
		}

	default:
		decl, _ := ctx.itext(false)
		return decl
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

func (ctx *ParseContext) vardecl() *ElemDecl {
	if !ctx.match('$') {
		ctx.addErr("vardecl does not start with '$'")
		return nil
	}
	name := ctx.varname()
	if name == "" {
		ctx.addErr("invalid varname in vardecl")
		return nil
	}
	ctx.ws()
	if !ctx.match('=') {
		ctx.addErr("invalid varname, or no '=' in vardecl")
		return nil
	}
	ctx.ws()
	val := string(ctx.Line[ctx.Pos:])
	ctx.Pos = len(ctx.Line)
	return &ElemDecl{VarName: name, VarValue: val}
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

func (ctx *ParseContext) varexpr() (string, bool) {
	if !ctx.match2('$', '{') {
		ctx.addErr("bad varexpr, must start with \"${\"")
		return "", false
	}
	formatSpec := "%v"
	varName := ctx.varname()
	if varName == "" {
		return "", false
	}
	if ctx.match(':') {
		if !ctx.match('%') {
			ctx.addErr("bad varexpr, format string must start with '%%'")
			return "", false
		}
		var buf bytes.Buffer
		buf.WriteByte('%')
		for {
			if ctx.iseof() {
				ctx.addErr("unterminated varexpr")
				return "", false
			}
			if ctx.test('}') {
				break
			}
			ch := ctx.advance()
			buf.WriteRune(ch)
		}
		formatSpec = buf.String()
	}
	if !ctx.match('}') {
		ctx.addErr("bad varexpr, must end with '}'")
		return "", false
	}
	varVal := ctx.VarFn(varName)
	if formatSpec == "%B" {
		var boolVal bool
		switch v := varVal.(type) {
		case bool:
			boolVal = v

		case string:
			if v != "" {
				boolVal = true
			}

		case int, int8, int16, int32, int64:
			if reflect.ValueOf(v).Int() != 0 {
				boolVal = true
			}

		case uint, uint8, uint16, uint32, uint64:
			if reflect.ValueOf(v).Uint() != 0 {
				boolVal = true
			}

		case float32, float64:
			if reflect.ValueOf(v).Float() != 0 {
				boolVal = true
			}
		}
		if boolVal {
			return "1", true
		}
		return "", true
	}
	str := fmt.Sprintf(formatSpec, varVal)
	return str, true
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
			varVal, ok := ctx.varexpr()
			if !ok {
				return "", false
			}
			buf.WriteString(varVal)
			continue
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
	if unicode.IsSpace(ch) || ch == ']' {
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

// returns (classnames, attrs, ok)
func (ctx *ParseContext) attrs() ([]string, map[string]string, bool) {
	if !ctx.match('[') {
		ctx.addErr("bad attribute, no opening '['")
		return nil, nil, false
	}
	var cns []string
	rtn := make(map[string]string)
	// classnames
	if ctx.isSimpleAlpha() {
		for {
			cn := ctx.varname()
			if cn == "" {
				return nil, nil, false
			}
			cns = append(cns, cn)
			if ctx.iseof() {
				ctx.addErr("unterminated attributes block")
				return nil, nil, false
			}
			ch := ctx.peek()
			if ch == ']' || unicode.IsSpace(ch) {
				break
			}
			if !ctx.match(':') {
				ctx.addErr("bad classname character")
				return nil, nil, false
			}
		}
	}
	// attrs
	for {
		ctx.ws()
		if ctx.iseof() {
			ctx.addErr("unterminated attributes block")
			return nil, nil, false
		}
		ch := ctx.peek()
		if ch == ']' {
			break
		}
		if !ctx.match('@') {
			ctx.addErr("attribute names must start with '@'")
			return nil, nil, false
		}
		attrName, attrVal, ok := ctx.attr()
		if !ok {
			return nil, nil, false
		}
		rtn[strings.ToLower(attrName)] = attrVal
	}
	if !ctx.match(']') {
		ctx.addErr("attributes not terminated correct with closing ']'")
		return nil, nil, false
	}
	return cns, rtn, true
}

func (ctx *ParseContext) itext(canMulti bool) (*ElemDecl, bool) {
	rtn := &ElemDecl{ElemType: "text"}
	if ctx.test('[') {
		var ok bool
		rtn.ClassNames, rtn.Attrs, ok = ctx.attrs()
		if !ok {
			return nil, false
		}
		ctx.ws()
	}
	var buf bytes.Buffer
	for {
		if ctx.iseof() {
			break
		}
		if ctx.test2('$', '{') {
			varVal, ok := ctx.varexpr()
			if !ok {
				return nil, false
			}
			buf.WriteString(varVal)
			continue
		}
		if canMulti && ctx.test2('|', '|') {
			break
		}
		ch := ctx.advance()
		buf.WriteRune(ch)
	}
	rtn.Text = buf.String()
	return rtn, true
}

func (ctx *ParseContext) mtext() (*ElemDecl, bool) {
	if !ctx.match('*') {
		ctx.addErr("multi-text must start with '*'")
		return nil, false
	}
	rtn := &ElemDecl{ElemType: "div", IsSelfClose: true}
	var ok bool
	rtn.ClassNames, rtn.Attrs, ok = ctx.attrs()
	ctx.ws()
	if !ok {
		return nil, false
	}
	for {
		decl, ok := ctx.itext(true)
		if !ok {
			return nil, false
		}
		decl.Text = strings.TrimSpace(decl.Text)
		rtn.List = append(rtn.List, decl)
		if ctx.iseof() {
			break
		}
		if !ctx.match2('|', '|') {
			ctx.addErr("bad mtext decl, separate with '||'")
			return nil, false
		}
		ctx.ws()
	}
	return rtn, true
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
		ctx.ws()
	}
	if ctx.test('"') {
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
		rtn.ClassNames, rtn.Attrs, ok = ctx.attrs()
		if !ok {
			return nil
		}
		ctx.ws()
	}
	if ctx.iseof() {
		return rtn
	}
	subElem, ok := ctx.itext(false)
	if !ok {
		return nil
	}
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
