package dash

import (
	"fmt"

	"github.com/sawka/dashborg-go-sdk/pkg/transport"
)

type Control struct {
	PanelName  string `json:"panelname"`
	ElemType   string `json:"elemtype"`
	ControlLoc string `json:"controlloc"`
}

func (c *Control) IsValid() bool {
	return c.ControlLoc != "" && c.ElemType != "" && c.ElemType != "invalid"
}

func (c *Control) OnClick(fn func() error) {
}

func (c *Control) ProgressSet(val int, status string) {
}

func (c *Control) ProgressDone() {
}

func (c *Control) ProgressError(err string) {
}

func (c *Control) LogText(fmtStr string, data ...interface{}) {
	if c.ElemType != "log" || !c.IsValid() {
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
	return nil
}

func (c *Control) RowDataClear() {
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
