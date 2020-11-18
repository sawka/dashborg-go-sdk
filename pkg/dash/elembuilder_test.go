package dash

import (
	"testing"
)

func mustSubElem(e *Elem) *Elem {
	if e == nil {
		return nil
	}
	return e.SubElem
}

func mustList(e *Elem, pos int) *Elem {
	if e == nil || len(e.List) <= pos {
		return nil
	}
	return e.List[pos]
}

func TestSimple(t *testing.T) {
	b := MakeElemBuilder("default", "/panel/default/default", 0)
	b.Print("$x = y")
	if b.Vars["x"] != "y" {
		t.Errorf("var not set correctly in builder")
	}
	p := b.Print("<progress/>")
	if p == nil {
		t.Errorf("no control returned from builder")
	}
	if p != nil && (p.ControlType != "progress") {
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

	b = MakeElemBuilder("default", "/panel/default/default", 0)
	p = b.Print("<progress/>", Attr("progressmax", "10"))
	elem := b.DoneElem()
	if elem == nil || len(elem.List) != 1 {
		t.Errorf("bad parse")
	} else {
		pelem := elem.List[0]
		if pelem.ElemType != "progress" {
			t.Errorf("not progress elem")
		} else if pelem.Attrs["progressmax"] != "10" {
			t.Errorf("did not process Attr argument")
		}
	}

	b = MakeElemBuilder("default", "/panel/default/default", 0)
	b.Print("<link/>[@test=\"hello ${name:%v}\"] Account #${AccId:%s}", Var("AccId", "187"), Var("name", "mike"))
	wrapElem := b.DoneElem()
	linkElem := mustList(wrapElem, 0)
	if linkElem == nil || len(elem.List) != 1 {
		t.Errorf("bad parse %v", b.Errs)
	}
	if linkElem != nil && linkElem.ElemType != "link" {
		t.Errorf("not link elem")
	}
	linkText := mustSubElem(linkElem)
	if linkText == nil {
		t.Errorf("bad parse, no linktext")
	}
	if linkText != nil && linkText.Text != "Account #187" {
		t.Errorf("bad var interpolation in text")
	}
	if linkElem != nil && linkElem.Attrs["test"] != "hello mike" {
		t.Errorf("bad var interpoliation in attr")
	}

	b = MakeElemBuilder("default", "/panel/default/default", 0)
	b.Print("*[@paddingtop=5px] [@bold @width=120px] Acc ID || ${accId:%s}", Var("accId", "187"))
	elem = b.DoneElem()
	if elem == nil || len(elem.List) != 2 {
		t.Errorf("bad parse %v", b.Errs)
	}
	if elem != nil && len(elem.List) == 2 && (elem.List[1].Text != "187") {
		t.Errorf("did not get correct text for 2nd list elem")
	}
	if elem != nil && len(elem.List) == 2 && (elem.Attrs["paddingtop"] != "5px" || elem.List[0].Attrs["width"] != "120px") {
		t.Errorf("bad attribute parse for mdiv")
	}
}
