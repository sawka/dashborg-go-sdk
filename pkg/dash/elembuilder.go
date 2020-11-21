package dash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"github.com/sawka/dashborg-go-sdk/pkg/parser"
)

// TODO check for input fields with same "formfield" name (warning)

type ElemBuilder struct {
	PanelName        string
	LocId            string
	ControlTs        int64
	Vars             map[string]interface{}
	Root             *Elem
	ImplicitRoot     bool
	Stack            []*Elem
	LastAppended     *Elem
	NoAnon           bool
	AllowBareControl bool
	LineNo           int
	Errs             []parser.ParseErr
	Warns            []parser.ParseErr
	RawLines         []string
	RootDivClass     string
}

type BuilderAttr struct {
	AttrName string
	AttrVal  string
}

type BuilderVar struct {
	VarName string
	VarVal  interface{}
}

type BuilderArg interface {
	IsBuilderArg() bool
}

func (_ BuilderAttr) IsBuilderArg() bool {
	return true
}

func (_ BuilderVar) IsBuilderArg() bool {
	return true
}

func Attr(name string, val string) BuilderAttr {
	return BuilderAttr{AttrName: name, AttrVal: val}
}

func Var(name string, val interface{}) BuilderVar {
	return BuilderVar{VarName: name, VarVal: val}
}

func JsonAttr(name string, val interface{}) BuilderAttr {
	if val == nil {
		return BuilderAttr{} // empty for nil
	}
	barr, err := json.Marshal(val)
	if err != nil {
		fmt.Printf("Dashborg JsonAttr, cannot marshal val type:%T err:%v", val, err)
		return BuilderAttr{}
	}
	return BuilderAttr{AttrName: name, AttrVal: string(barr)}
}

func MakeElemBuilder(panelName string, locId string, controlTs int64) *ElemBuilder {
	return &ElemBuilder{
		PanelName: panelName,
		LocId:     locId,
		Vars:      make(map[string]interface{}),
		ControlTs: controlTs,
	}
}

func (b *ElemBuilder) TrackAnonControls(anonTrack bool) {
	b.NoAnon = !anonTrack
}

func (b *ElemBuilder) SetAllowBareControl(allow bool) {
	b.AllowBareControl = allow
}

func (b *ElemBuilder) SetRootDivClass(cn string) {
	b.RootDivClass = cn
}

func (b *ElemBuilder) SetVar(name string, val interface{}) {
	if val == nil {
		delete(b.Vars, name)
		return
	}
	b.Vars[name] = val
}

func tempVarsFromArgs(args []BuilderArg) map[string]interface{} {
	var rtn map[string]interface{}
	for _, arg := range args {
		if bvar, ok := arg.(BuilderVar); ok {
			if rtn == nil {
				rtn = make(map[string]interface{})
			}
			rtn[bvar.VarName] = bvar.VarVal
		}
	}
	return rtn
}

func addArgAttrs(attrs map[string]string, args []BuilderArg) map[string]string {
	for _, arg := range args {
		if battr, ok := arg.(BuilderAttr); ok {
			if attrs == nil {
				attrs = make(map[string]string)
			}
			if battr.AttrName == "" {
				continue
			}
			attrs[battr.AttrName] = battr.AttrVal
		}
	}
	return attrs
}

func (b *ElemBuilder) ReportErrors(w io.Writer) {
	if len(b.Errs) == 0 {
		return
	}
	sort.Slice(b.Errs, func(i int, j int) bool {
		return b.Errs[i].LineNo < b.Errs[j].LineNo
	})
	for _, err := range b.Errs {
		var line string
		if err.LineNo > 0 {
			line = b.RawLines[err.LineNo-1]
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
		}
		fmt.Fprintf(w, "Line %3d | %s\n", err.LineNo, line)
		fmt.Fprintf(w, "* ERROR col:%d %s\n", err.Col, err.Err)
	}
}

func (b *ElemBuilder) HasErrors() bool {
	return len(b.Errs) > 0
}

func (b *ElemBuilder) PrintMulti(text string) {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		b.Print(line)
	}
}

func (b *ElemBuilder) Print(text string, args ...BuilderArg) *Control {
	b.RawLines = append(b.RawLines, text)
	b.LineNo++
	if strings.TrimSpace(text) == "" {
		return nil
	}
	tempVars := tempVarsFromArgs(args)
	ctx := parser.MakeParseContext(text, b.LineNo, parser.Map2VarFn(b.Vars, tempVars))
	edecl := ctx.ParseLine()
	for _, err := range ctx.Errs {
		b.Errs = append(b.Errs, err)
	}
	if edecl == nil {
		return nil
	}
	if edecl.VarName != "" {
		// vardecl
		b.Vars[edecl.VarName] = edecl.VarValue
		return nil
	}
	edecl.Attrs = addArgAttrs(edecl.Attrs, args)
	if edecl.IsClose {
		b.closeTag(edecl)
		return nil
	}
	elem := b.declToElem(edecl)
	if elem == nil {
		return nil
	}
	if b.Root == nil && elem.ElemType == "div" {
		b.Root = elem
		b.Stack = []*Elem{elem}
		return nil
	}
	if b.Root == nil {
		b.Root = &Elem{ElemType: "div"}
		if b.RootDivClass != "" {
			b.Root.ClassNames = []string{b.RootDivClass}
		}
		b.Stack = []*Elem{b.Root}
		b.ImplicitRoot = true
	}
	if len(b.Stack) == 0 {
		if b.ImplicitRoot {
			panic("ElemBuilder Stack should never be empty with ImplicitRoot")
		}
		// swap to ImplicitRoot
		oldRoot := b.Root
		b.Root = &Elem{ElemType: "div"}
		if b.RootDivClass != "" {
			b.Root.ClassNames = []string{b.RootDivClass}
		}
		b.Stack = []*Elem{b.Root}
		b.Root.List = []*Elem{oldRoot}
		b.ImplicitRoot = true
	}
	b.append(elem, edecl.IsSelfClose)
	if elem.ControlLoc != "" {
		return &Control{ControlType: elem.ElemType, ControlLoc: elem.ControlLoc, PanelName: b.PanelName}
	}
	return nil
}

func (b *ElemBuilder) DoneElem() *Elem {
	if (b.ImplicitRoot && len(b.Stack) > 1) || (!b.ImplicitRoot && len(b.Stack) > 0) {
		b.addWarn("some tags were left unclosed")
	}
	if b.AllowBareControl && b.ImplicitRoot && b.Root != nil && len(b.Root.List) == 1 {
		return b.Root.List[0]
	}
	return b.Root
}

func (b *ElemBuilder) declToElem(edecl *parser.ElemDecl) *Elem {
	if edecl == nil {
		return nil
	}
	meta := CMeta[edecl.ElemType]
	if meta == nil {
		return nil
	}
	if edecl.ElemSubType != "" && !meta.AllowedSubTypes[edecl.ElemSubType] {
		return nil
	}
	rtn := &Elem{
		ElemType:    edecl.ElemType,
		ElemSubType: edecl.ElemSubType,
		ClassNames:  edecl.ClassNames,
		Attrs:       edecl.Attrs,
	}
	if meta.HasControl {
		if edecl.ControlName != "" {
			rtn.ControlName = edecl.ControlName
		}
		if b.LocId == "" {
			rtn.ControlLoc = dashutil.INVALID_CLOC
		} else if edecl.ControlId != "" {
			if b.ControlTs != 0 {
				rtn.ControlLoc = b.LocId + "|" + edecl.ControlId + "|" + strconv.FormatInt(b.ControlTs, 10)
			} else {
				rtn.ControlLoc = b.LocId + "|" + edecl.ControlId
			}
		}
		if rtn.ControlName == "" && rtn.ControlLoc == "" {
			if b.ControlTs != 0 {
				rtn.ControlLoc = b.LocId + "|" + uuid.New().String() + "|" + strconv.FormatInt(b.ControlTs, 10)
			} else {
				rtn.ControlLoc = b.LocId + "|" + uuid.New().String()
			}
		}
	}
	if meta.SubElemType == SUBELEM_TEXT {
		rtn.Text = edecl.Text
		if edecl.Text == "" && edecl.SubElem != nil && edecl.SubElem.ElemType == "text" {
			rtn.Text = edecl.SubElem.Text
		}
	} else if edecl.IsSelfClose {
		// subelems are only set for self closing tags
		if meta.SubElemType == SUBELEM_ONE {
			rtn.SubElem = b.declToElem(edecl.SubElem)
		} else if meta.SubElemType == SUBELEM_LIST {
			if edecl.SubElem != nil {
				rtn.List = []*Elem{b.declToElem(edecl.SubElem)}
			} else if len(edecl.List) > 0 {
				rtn.List = make([]*Elem, 0, len(edecl.List))
				for _, subDecl := range edecl.List {
					e := b.declToElem(subDecl)
					if e != nil {
						rtn.List = append(rtn.List, e)
					}
				}
			}
		}
	}
	return rtn
}

func (b *ElemBuilder) stackTop() *Elem {
	if len(b.Stack) == 0 {
		return nil
	}
	return b.Stack[len(b.Stack)-1]
}

func (b *ElemBuilder) addErr(fmtStr string, args ...interface{}) {
	err := fmt.Sprintf(fmtStr, args...)
	b.Errs = append(b.Errs, parser.ParseErr{LineNo: b.LineNo, Err: err})
}

func (b *ElemBuilder) addWarn(fmtStr string, args ...interface{}) {
	err := fmt.Sprintf(fmtStr, args...)
	b.Warns = append(b.Errs, parser.ParseErr{LineNo: b.LineNo, Err: err})
}

func (b *ElemBuilder) closeTag(edecl *parser.ElemDecl) {
	top := b.stackTop()
	if top == nil {
		b.addErr("Cannot close tag (no open tags)")
		return
	}
	if top.ElemType != edecl.ElemType {
		b.addErr("Cannot close tag, tag types don't match")
		return
	}
	if edecl.ElemSubType != "" && top.ElemSubType != edecl.ElemSubType {
		b.addErr("Cannot close tag, tag subtypes don't match")
		return
	}
	if b.ImplicitRoot && len(b.Stack) == 1 {
		b.addErr("Cannot close tag (no open tags)")
		return
	}
	// you can close a non-implicit root, so len(b.Stack) == 0
	b.Stack = b.Stack[:len(b.Stack)-1]
}

func (b *ElemBuilder) maybePush(elem *Elem, selfClose bool) {
	if selfClose {
		return
	}
	meta := elem.GetMeta()
	if meta.SubElemType != SUBELEM_ONE && meta.SubElemType != SUBELEM_LIST {
		return
	}
	b.Stack = append(b.Stack, elem)
}

func (b *ElemBuilder) append(elem *Elem, selfClose bool) {
	top := b.stackTop()
	if top == nil {
		panic("ElemBuilder.append should never see an empty stack")
	}
	topMeta := top.GetMeta()
	if topMeta.SubElemType == SUBELEM_ONE {
		if top.SubElem != nil {
			b.addWarn("Tag can only have one child, overwriting previous tag")
		}
		top.SubElem = elem
		b.maybePush(elem, selfClose)
	} else if topMeta.SubElemType == SUBELEM_LIST {
		top.List = append(top.List, elem)
		b.maybePush(elem, selfClose)
	} else {
		panic(fmt.Sprintf("ElemBuilder.append top of stack has ElemType[%s], cannot append", top.ElemType))
	}
}

// returns true if there was output, false if none
func (e *Elem) writeAttrsStr(buf *bytes.Buffer) bool {
	if len(e.ClassNames) == 0 && len(e.Attrs) == 0 {
		return false
	}
	buf.WriteByte('[')
	for idx, cn := range e.ClassNames {
		if idx != 0 {
			buf.WriteByte(':')
		}
		buf.WriteString(cn)
	}
	if len(e.ClassNames) > 0 && len(e.Attrs) > 0 {
		buf.WriteByte(' ')
	}
	attrIdx := 0
	attrNames := make([]string, 0, len(e.Attrs))
	for name, _ := range e.Attrs {
		// need a stable order for elemhash
		attrNames = append(attrNames, name)
	}
	sort.Strings(attrNames)
	for _, name := range attrNames {
		val := e.Attrs[name]
		escVal := strings.ReplaceAll(val, "\\", "\\\\")
		escVal = strings.ReplaceAll(escVal, "\"", "\\\"")
		if attrIdx != 0 {
			buf.WriteByte(' ')
		}
		buf.WriteByte('@')
		buf.WriteString(name)
		if escVal != "1" {
			buf.WriteByte('=')
			buf.WriteByte('"')
			buf.WriteString(escVal)
			buf.WriteByte('"')
		}
		attrIdx++
	}
	buf.WriteByte(']')
	return true
}

func (e *Elem) writeTextElem(buf *bytes.Buffer) {
	wroteAttrs := e.writeAttrsStr(buf)
	if wroteAttrs {
		buf.WriteByte(' ')
	}
	buf.WriteString(e.Text)
	return
}

// pass negative indentSize for no indenting
func (e *Elem) ElemTextEx(indentSize int, et []string) []string {
	if e == nil {
		return nil
	}
	var buf bytes.Buffer
	for i := 0; i < indentSize; i++ {
		buf.WriteByte(' ')
	}
	if e.ElemType == "text" {
		e.writeTextElem(&buf)
		et = append(et, buf.String())
		return et
	}
	hasSubElems := e.SubElem != nil || len(e.List) > 0
	textSubElem := e.SubElem != nil && e.SubElem.ElemType == "text"
	isSelfClose := textSubElem || !hasSubElems

	buf.WriteByte('<')
	buf.WriteString(e.ElemType)
	if e.ElemSubType != "" {
		buf.WriteByte(':')
		buf.WriteString(e.ElemSubType)
	}
	if e.ControlLoc != "" {
		cloc, err := dashutil.ParseControlLocator(e.ControlLoc)
		if err == nil {
			buf.WriteString(" *")
			buf.WriteString(cloc.ControlId)
		}
	}
	if e.ControlName != "" {
		buf.WriteString(" \"")
		buf.WriteString(e.ControlName)
		buf.WriteString("\"")
	}
	if isSelfClose {
		buf.WriteByte('/')
	}
	buf.WriteByte('>')
	wroteAttrs := e.writeAttrsStr(&buf)
	if e.Text != "" {
		if wroteAttrs {
			buf.WriteByte(' ')
		}
		buf.WriteString(e.Text)
		et = append(et, buf.String())
		return et
	}
	if textSubElem {
		if wroteAttrs {
			buf.WriteByte(' ')
		}
		e.SubElem.writeTextElem(&buf)
		et = append(et, buf.String())
		return et
	}
	if hasSubElems {
		et = append(et, buf.String())
		newIndentSize := indentSize
		if indentSize >= 0 {
			newIndentSize += 2
		}
		for _, se := range e.List {
			et = se.ElemTextEx(newIndentSize, et)
		}
		var closeTagBuf bytes.Buffer
		for i := 0; i < indentSize; i++ {
			closeTagBuf.WriteByte(' ')
		}
		closeTagBuf.WriteString("</")
		closeTagBuf.WriteString(e.ElemType)
		closeTagBuf.WriteString(">")
		et = append(et, closeTagBuf.String())
		return et
	}
	et = append(et, buf.String())
	return et
}

func (e *Elem) Dump(w io.Writer) {
	elemText := e.ElemTextEx(0, nil)
	fmt.Fprintf(w, "%s\n", strings.Join(elemText, "\n"))
}
