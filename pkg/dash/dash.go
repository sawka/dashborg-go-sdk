package dash

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const (
	SUBELEM_TEXT = "T"
	SUBELEM_ONE  = "1"
	SUBELEM_LIST = "*"
)

type ControlTypeMeta struct {
	CanInline       bool
	CanEmbed        bool
	HasControl      bool
	HasData         bool
	HasRowData      bool
	HasEph          bool
	HasSubControls  bool
	SubElemType     string
	AllowedSubTypes map[string]bool
}

func (ctm *ControlTypeMeta) CanOpen() bool {
	return ctm.SubElemType == SUBELEM_ONE || ctm.SubElemType == SUBELEM_LIST
}

var CMeta map[string]*ControlTypeMeta

func init() {
	CMeta = make(map[string]*ControlTypeMeta)
	CMeta["text"] = makeCTM("inline embed sub-T")
	CMeta["hr"] = makeCTM("embed")
	CMeta["div"] = makeCTM("embed sub-*")
	CMeta["link"] = makeCTM("inline embed sub-1")
	CMeta["youtube"] = makeCTM("embed")
	CMeta["dyntext"] = makeCTM("inline embed control hasdata sub-T")
	CMeta["dynelem"] = makeCTM("embed control hasdata sub-1")
	CMeta["image"] = makeCTM("inline embed")
	CMeta["log"] = makeCTM("rowdata control subctl")
	CMeta["button"] = makeCTM("inline embed control sub-1")
	CMeta["context"] = makeCTM("control sub-* eph")
	CMeta["progress"] = makeCTM("inline embed control hasdata")
	CMeta["handler"] = makeCTM("control")
	CMeta["counter"] = makeCTM("inline embed control hasdata")
	CMeta["input"] = makeCTM("inline embed")
	CMeta["inputselect"] = makeCTM("inline embed control rowdata")
	CMeta["table"] = makeCTM("embed control rowdata subctl sub-*")
	CMeta["datatable"] = makeCTM("embed control rowdata sub-*")
	CMeta["th"] = makeCTM("sub-*")
	CMeta["tdformat"] = makeCTM("sub-*")

	CMeta["context"].AllowedSubTypes = map[string]bool{"context": true, "modal": true}
	CMeta["progress"].AllowedSubTypes = map[string]bool{"bar": true, "spinner": true}
	CMeta["input"].AllowedSubTypes = map[string]bool{"text": true, "hidden": true, "checkbox": true, "switch": true, "date": true, "time": true, "datetime": true}
	CMeta["inputselect"].AllowedSubTypes = map[string]bool{"select": true, "combo": true, "search": true}
}

func makeCTM(text string) *ControlTypeMeta {
	rtn := &ControlTypeMeta{}
	rtn.CanInline = strings.Contains(text, "inline")
	rtn.CanEmbed = strings.Contains(text, "embed")
	rtn.HasControl = strings.Contains(text, "control")
	rtn.HasData = strings.Contains(text, "hasdata")
	rtn.HasRowData = strings.Contains(text, "rowdata")
	rtn.HasEph = strings.Contains(text, "eph")
	rtn.HasSubControls = strings.Contains(text, "subctl")
	if strings.Contains(text, "sub-T") {
		rtn.SubElemType = "T"
	}
	if strings.Contains(text, "sub-1") {
		rtn.SubElemType = "1"
	}
	if strings.Contains(text, "sub-*") {
		rtn.SubElemType = "*"
	}
	return rtn
}

type Config struct {
	AccId    string // DASHBORG_ACCID
	AnonAcc  bool
	ZoneName string // DASHBORG_ZONE
	Env      string // DASHBORG_ENV default is "prod".  can be set to "dev"

	BufSrvHost string // DASHBORG_PROCHOST
	BufSrvPort int    // DASHBORG_PROCPORT

	ProcName  string // DASHBORG_PROCNAME
	ProcIName string
	ProcTags  map[string]string
	ProcINum  int

	KeyFileName     string // DASHBORG_KEYFILE
	CertFileName    string // DASHBORG_CERTFILE
	AutoKeygen      bool
	Verbose         bool // DASHBORG_VERBOSE
	MinClearTimeout time.Duration
}

type Elem struct {
	ElemType    string            `json:"elemtype"`
	ElemSubType string            `json:"elemsubtype,omitempty"`
	ClassNames  []string          `json:"classnames,omitempty"`
	Attrs       map[string]string `json:"attrs,omitempty"`
	ControlName string            `json:"controlname,omitempty"`
	ControlLoc  string            `json:"controlloc,omitempty"`
	Text        string            `json:"text,omitempty"`
	SubElem     *Elem             `json:"subelem,omitempty"`
	List        []*Elem           `json:"list,omitempty"`
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
	for name, val := range e.Attrs {
		escVal := strings.ReplaceAll(val, "\\", "\\\\")
		escVal = strings.ReplaceAll(val, "\"", "\\\"")
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

func (e *Elem) elemTextEx(indent string, et []string) []string {
	var buf bytes.Buffer
	meta := CMeta[e.ElemType]
	if meta == nil {
		panic(fmt.Sprintf("dashborg.ElemText() bad ElemType:%s\n", e.ElemType))
	}
	buf.WriteString(indent)
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
	} else if e.ControlName != "" {
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
		newIndent := indent + "  "
		for _, se := range e.List {
			et = se.elemTextEx(newIndent, et)
		}
		closeTag := fmt.Sprintf("%s</%s>", indent, e.ElemType)
		et = append(et, closeTag)
		return et
	}
	et = append(et, buf.String())
	return et
}

func (e *Elem) Dump() {
	elemText := e.elemTextEx("", nil)
	fmt.Printf("%s\n", strings.Join(elemText, "\n"))
}

// Elem
//
// elemtype
// elemsubtype
// controlid

type IPanelRequest interface {
	LookupContext(name string) IContextWriter
	GetHandlerPath() string
	GetData() interface{}
}

type IContextWriter interface {
	// IElemBuilder
	Flush()
	Revert()
}

type PanelWriter struct {
	*ElemBuilder
	PanelName string
}

func DefinePanel(panelName string) *PanelWriter {
	rtn := &PanelWriter{PanelName: panelName}
	rtn.ElemBuilder = MakeElemBuilder(dashutil.MakeZPLocId(Client.Config.ZoneName, panelName))
	return rtn
}

func (p *PanelWriter) Flush() {

}

func (p *PanelWriter) PanelLink() string {
	return panelLink(Client.Config.AccId, Client.Config.ZoneName, p.PanelName)
}

type Panel struct {
	PanelName string
}

func LookupPanel(panelName string) *Panel {
	return &Panel{PanelName: panelName}
}

func panelLink(accId string, zoneName string, panelName string) string {
	if panelName == "" {
		panelName = "default"
	}
	if Client.Config.BufSrvHost == "localhost" {
		return fmt.Sprintf("http://console-dashborg.localdev:8080#acc:%s,zone:%s,panel:%s", accId, zoneName, panelName)
	} else {
		return fmt.Sprintf("https://console.dashborg.net/#acc:%s,zone:%s,panel:%s", accId, zoneName, panelName)
	}
}

func (p *Panel) PanelLink() string {
	return panelLink(Client.Config.AccId, Client.Config.ZoneName, p.PanelName)
}

func (p *Panel) LookupControl(controlType string, controlName string) *Control {
	return nil
}

type IZone interface {
	LookupControl(controlType string, controlName string) Control
}

type IControl interface {
	OnClick(fn func() error)

	ProgressSet(val int, status string)
	ProgressDone()
	ProgressError(err string)

	LogText(fmt string, data ...interface{})
	LogControl(text string, attrs ...string) Control

	RowDataClear()

	DynSetFStr(fmt string)
	DynSetData(data ...interface{})
	DynSetElem(elemtext []string)

	OnAllRequests(fn func(req IPanelRequest) (bool, error))
	OnRequest(path string, fn func(req IPanelRequest) error)

	CounterInc(val int)

	TableAddData(data ...interface{})
	TableAddElems(elemtext []string)
}

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
}

func LogInfo(fmt string, data ...interface{}) {
	if Client.Config.Verbose {
		log.Printf(fmt, data...)
	}
}
