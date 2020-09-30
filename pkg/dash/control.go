package dash

type Control struct {
	ElemType   string `json:"elemtype"`
	ControlLoc string `json:"controlloc"`
}

func (c *Control) OnClick(fn func() error) {
}

func (c *Control) ProgressSet(val int, status string) {
}

func (c *Control) ProgressDone() {
}

func (c *Control) ProgressError(err string) {
}

func (c *Control) LogText(fmt string, data ...interface{}) {
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
