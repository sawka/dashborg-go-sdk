package dash

import (
	"fmt"
	"os"

	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"github.com/sawka/dashborg-go-sdk/pkg/transport"
)

type Control struct {
	ClientId    string `json:"clientid"`
	PanelName   string `json:"panelname"`
	ControlType string `json:"controltype"` // elemtype
	ControlLoc  string `json:"controlloc"`
}

func (c *Control) IsValid() bool {
	return c.ControlLoc != "" && c.ControlType != "" && c.ControlType != "invalid"
}

func (c *Control) GetMeta() *ControlTypeMeta {
	if c == nil {
		return &ControlTypeMeta{}
	}
	meta := CMeta[c.ControlType]
	if meta == nil {
		meta = &ControlTypeMeta{}
	}
	return meta
}

func (c *Control) GenericUpdate(cmd string, data interface{}) {
	if !c.IsValid() || !c.GetMeta().HasData {
		return
	}
	m := transport.ControlUpdateMessage{
		MType:      "controlupdate",
		Ts:         Ts(),
		ControlLoc: c.ControlLoc,
		Cmd:        cmd,
		Data:       data,
	}
	Client.SendMessage(m)
}

func (c *Control) OnClick(fn func() error) {
	if c.ControlType != "button" || !c.IsValid() {
		return
	}
	runFn := func(v interface{}) (interface{}, error) {
		err := fn()
		if err != nil {
			return nil, err
		}
		return true, nil
	}
	Client.RegisterPushFn(c.ControlLoc, runFn, false)
}

func (c *Control) Release() {
	if c.ClientId != "" {
		Client.UntrackActive(c.ControlType, c.ControlLoc, c.ClientId)
	}
	c.ClientId = ""
	c.ControlType = "invalid"
	c.ControlLoc = ""

	// TODO release handlers (onclick, onrequest, etc.)
}

func (c *Control) ProgressSet(val int, status string) {
	if c.ControlType != "progress" || !c.IsValid() {
		return
	}
	c.GenericUpdate("genupdate", transport.ProgressData{Val: val, Status: status})
}

func (c *Control) ProgressDone() {
	if c.ControlType != "progress" || !c.IsValid() {
		return
	}
	c.GenericUpdate("genupdate", transport.ProgressData{Done: true, ClearStatus: true})
}

func (c *Control) ProgressError(err string) {
	if c.ControlType != "progress" || !c.IsValid() {
		return
	}
	c.GenericUpdate("genupdate", transport.ProgressData{Done: true, ClearStatus: true, Err: err})
}

func (c *Control) LogText(fmtStr string, data ...interface{}) {
	if c.ControlType != "log" || !c.IsValid() {
		return
	}
	text := fmt.Sprintf(fmtStr, data...)
	ts := Ts()
	entry := transport.LogEntry{
		Ts:        ts,
		ProcRunId: Client.GetProcRunId(),
		Text:      text,
	}
	m := transport.ControlAppendMessage{
		MType:      "controlappend",
		Ts:         ts,
		PanelName:  c.PanelName,
		ControlLoc: c.ControlLoc,
		Data:       entry,
	}
	Client.SendMessage(m)
}

func (c *Control) LogControl(text string, attrs ...string) *Control {
	if c.ControlType != "log" || !c.IsValid() {
		return nil
	}

	cloc := dashutil.MustParseControlLocator(c.ControlLoc)
	elem := ParseElemText([]string{text}, dashutil.MakeEphScLocId(cloc.ControlId), false)
	if elem == nil {
		return nil
	}
	fmt.Printf("logcontrol elem:\n")
	elem.Dump(os.Stdout)
	fmt.Printf("elem cloc:%s\n", elem.ControlLoc)
	if !elem.GetMeta().CanEmbed {
		return nil
	}
	ts := Ts()
	entry := transport.LogEntry{
		Ts:        ts,
		ProcRunId: Client.GetProcRunId(),
		ElemText:  elem.ElemTextEx(0, nil),
	}
	m := transport.ControlAppendMessage{
		MType:      "controlappend",
		Ts:         ts,
		PanelName:  c.PanelName,
		ControlLoc: c.ControlLoc,
		Data:       entry,
	}
	Client.SendMessage(m)
	if elem.ControlLoc != "" {
		return &Control{PanelName: c.PanelName, ControlType: elem.ElemType, ControlLoc: elem.ControlLoc}
	}
	return nil
}

func (c *Control) RowDataClear() {
	if !c.GetMeta().HasRowData {
		return
	}
	ts := Ts()
	rdata := transport.ResetTsData{
		ResetTs: ts,
	}
	m := transport.ControlUpdateMessage{
		MType:      "controlupdate",
		Cmd:        "clearrowdata",
		Ts:         ts,
		ControlLoc: c.ControlLoc,
		Data:       rdata,
	}
	Client.SendMessage(m)
}

func (c *Control) DynSetFStr(fmt string) {
}

func (c *Control) DynSetData(data ...interface{}) {
}

func (c *Control) DynSetElem(elemtext []string) {
}

func (c *Control) OnAllRequests(fn func(req IPanelRequest) (bool, error)) {
}

func (c *Control) OnRequest(path string, fn func(req IPanelRequest) error) {
}

func (c *Control) CounterInc(val int) {
}

func (c *Control) TableAddData(data ...interface{}) {
}

func (c *Control) TableAddElems(elemtext []string) {
}
