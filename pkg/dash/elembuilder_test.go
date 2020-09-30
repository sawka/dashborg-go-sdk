package dash

import (
	"testing"
)

func TestSimple(t *testing.T) {
	b := MakeElemBuilder("/panel/default/default")
	b.Print("$x = y")
	if b.Vars["x"] != "y" {
		t.Errorf("var not set correctly in builder")
	}
	p := b.Print("<progress/>")
	if p == nil {
		t.Errorf("no control returned from builder")
	}
	if p != nil && (p.ElemType != "progress") {
		t.Errorf("wrong control returned from builder")
	}
	if p != nil && p.ControlLoc == "" {
		t.Errorf("controlloc not set for anonymous progress control")
	}
	p2 := b.Print("<progress p-1/>")
	if p2 != nil {
		t.Errorf("control should not be returned for named control")
	}
	if len(b.Stack) != 1 {
		t.Errorf("builder, wrong stack size")
	}
	if len(b.Stack) == 1 && b.Stack[0].ElemType != "div" || !b.ImplicitRoot {
		t.Errorf("builder, implicit root not set correctly")
	}
	b.Print("<div>")
	if len(b.Stack) != 2 {
		t.Errorf("div was not pushed correctly to stack")
	}
	b.Print("hello")
	if e := b.stackTop(); e == nil || e.ElemType != "div" && len(e.List) != 1 {
		t.Errorf("text was not pushed correctly onto div")
	}
	b.Print("</div>")
	if len(b.Stack) != 1 {
		t.Errorf("div was not popped correctly")
	}
	b.DoneElem().Dump("")
}
