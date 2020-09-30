package dash

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/parser"
)

type ElemBuilder struct {
	LocId        string
	Vars         map[string]string
	Root         *Elem
	ImplicitRoot bool
	Stack        []*Elem
	LastAppended *Elem
	NoAnon       bool
	LineNo       int
	Errs         []parser.ParseErr
	Warns        []parser.ParseErr
}

func MakeElemBuilder(locId string) *ElemBuilder {
	return &ElemBuilder{LocId: locId, Vars: make(map[string]string)}
}

func (b *ElemBuilder) TrackAnonControls(anonTrack bool) {
	b.NoAnon = !anonTrack
}

func (b *ElemBuilder) SetVar(name string, val string) {
	if val == "" {
		delete(b.Vars, name)
		return
	}
	b.Vars[name] = val
}

func (b *ElemBuilder) Print(text string, attrs ...string) *Control {
	b.LineNo++
	ctx := parser.MakeParseContext(text, b.LineNo, b.Vars)
	edecl := ctx.ParseLine()
	for _, err := range ctx.Errs {
		b.Errs = append(b.Errs, err)
	}
	if edecl == nil {
		return nil
	}
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
		b.Root = &Elem{ElemType: "div", ClassNames: []string{"rootdiv"}}
		b.Stack = []*Elem{b.Root}
		b.ImplicitRoot = true
	}
	if len(b.Stack) == 0 {
		if b.ImplicitRoot {
			panic("ElemBuilder Stack should never be empty with ImplicitRoot")
		}
		// swap to ImplicitRoot
		oldRoot := b.Root
		b.Root = &Elem{ElemType: "div", ClassNames: []string{"rootdiv"}}
		b.Stack = []*Elem{b.Root}
		b.Root.List = []*Elem{oldRoot}
		b.ImplicitRoot = true
	}
	b.append(elem, edecl.IsSelfClose)
	if elem.ControlLoc != "" {
		return &Control{ElemType: elem.ElemType, ControlLoc: elem.ControlLoc}
	}
	return nil
}

func (b *ElemBuilder) DoneElem() *Elem {
	if (b.ImplicitRoot && len(b.Stack) > 1) || (!b.ImplicitRoot && len(b.Stack) > 0) {
		b.addWarn("some tags were left unclosed")
	}
	return b.Root
}

func (b *ElemBuilder) declToElem(edecl *parser.ElemDecl) *Elem {
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
	if meta.HasControl && edecl.ControlName != "" {
		rtn.ControlName = edecl.ControlName
	} else if meta.HasControl && edecl.ControlId != "" {
		rtn.ControlLoc = b.LocId + "|" + edecl.ControlId
	} else if meta.HasControl {
		rtn.ControlLoc = b.LocId + "|" + uuid.New().String()
	}
	if meta.SubElemType == SUBELEM_TEXT {
		rtn.Text = edecl.Text
	} else if edecl.IsSelfClose {
		// subelems are only set for self closing tags
		if meta.SubElemType == SUBELEM_ONE {
			rtn.SubElem = b.declToElem(edecl.SubElem)
		} else if meta.SubElemType == SUBELEM_LIST {
			rtn.List = []*Elem{b.declToElem(edecl.SubElem)}
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
	meta := CMeta[elem.ElemType]
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
	topMeta := CMeta[top.ElemType]
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
