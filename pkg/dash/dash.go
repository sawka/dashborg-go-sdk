package dash

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"github.com/sawka/dashborg-go-sdk/pkg/transport"
)

const (
	SUBELEM_TEXT = "T"
	SUBELEM_ONE  = "1"
	SUBELEM_LIST = "*"
)

type ControlTypeMeta struct {
	IsValid         bool
	CanInline       bool
	CanEmbed        bool
	HasControl      bool
	HasData         bool
	HasRowData      bool
	HasEph          bool
	HasSubControls  bool
	TrackActive     bool
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
	CMeta["log"] = makeCTM("rowdata control hasdata active subctl")
	CMeta["button"] = makeCTM("inline embed control active sub-1")
	CMeta["context"] = makeCTM("control sub-* eph")
	CMeta["progress"] = makeCTM("inline embed control active hasdata")
	CMeta["handler"] = makeCTM("control active")
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
	rtn.IsValid = true
	rtn.CanInline = strings.Contains(text, "inline")
	rtn.CanEmbed = strings.Contains(text, "embed")
	rtn.HasControl = strings.Contains(text, "control")
	rtn.HasData = strings.Contains(text, "hasdata")
	rtn.HasRowData = strings.Contains(text, "rowdata")
	rtn.HasEph = strings.Contains(text, "eph")
	rtn.HasSubControls = strings.Contains(text, "subctl")
	rtn.TrackActive = strings.Contains(text, "active")
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

	// used by server
	AnonProcRunId string `json:"anonprocrunid,omitempty"`
}

func (e *Elem) GetMeta() *ControlTypeMeta {
	if e == nil {
		return &ControlTypeMeta{}
	}
	meta := CMeta[e.ElemType]
	if meta == nil {
		meta = &ControlTypeMeta{}
	}
	return meta
}

func (e *Elem) Walk(visitFn func(*Elem)) {
	if e == nil {
		return
	}
	visitFn(e)
	if e.SubElem != nil {
		e.SubElem.Walk(visitFn)
	}
	for _, se := range e.List {
		se.Walk(visitFn)
	}
}

type PanelRequest struct {
	ZoneName    string
	PanelName   string
	FeClientId  string
	ReqId       string
	HandlerPath string
	Data        interface{}
	Depth       int
}

func (req *PanelRequest) LookupContext(name string) *ContextWriter {
	return nil
}

func (req *PanelRequest) GetHandlerPath() string {
	return req.HandlerPath
}

func (req *PanelRequest) GetData() interface{} {
	return req.Data
}

func (req *PanelRequest) TriggerRequest(handlerPath string, data interface{}) {
}

type ContextWriter struct {
	*ElemBuilder
	Req         *PanelRequest
	ContextElem *Elem
}

func (w *ContextWriter) Flush() {
}

func (w *ContextWriter) Revert() {
}

type PanelWriter struct {
	*ElemBuilder
	PanelName string
}

func ParseElemText(elemText []string, locId string, allowImplicitRoot bool) *Elem {
	if len(elemText) == 0 {
		return nil
	}
	b := MakeElemBuilder(locId)
	for _, text := range elemText {
		b.Print(text)
	}
	elem := b.DoneElem()
	if !allowImplicitRoot && b.ImplicitRoot {
		if len(elem.List) == 0 {
			return nil
		}
		return elem.List[0]
	}
	return elem
}

func DefinePanel(panelName string) *PanelWriter {
	rtn := &PanelWriter{PanelName: panelName}
	rtn.ElemBuilder = MakeElemBuilder(dashutil.MakeZPLocId(Client.Config.ZoneName, panelName))
	return rtn
}

func (p *PanelWriter) Flush() error {
	m := transport.DefinePanelMessage{
		MType:     "definepanel",
		Ts:        Ts(),
		ZoneName:  Client.Config.ZoneName,
		PanelName: p.PanelName,
		TrackAnon: !p.ElemBuilder.NoAnon,
		ElemText:  p.DoneText(),
	}
	Client.SendMessageWithCallback(m, func(rtn interface{}, err error) {
		fmt.Printf("DefinePanel err:%v rtn:%v\n", rtn, err)
	})
	return nil
}

func (p *PanelWriter) Dump(w io.Writer) {
	p.DoneElem().Dump(w)
}

func (p *PanelWriter) DoneText() []string {
	e := p.DoneElem()
	if e == nil {
		return nil
	}
	return e.ElemTextEx(0, nil)
}

func (p *PanelWriter) PanelLink() string {
	return panelLink(Client.Config.AccId, Client.Config.ZoneName, p.PanelName)
}

type Panel struct {
	PanelName       string
	ControlMappings map[string]*Control
}

func LookupPanel(panelName string) (*Panel, error) {
	p := &Panel{PanelName: panelName}
	err := p.RefreshControlMappings()
	return p, err
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

func (p *Panel) RefreshControlMappings() error {
	lpm := transport.LookupPanelMessage{
		MType:     "lookuppanel",
		Ts:        Ts(),
		ZoneName:  Client.Config.ZoneName,
		PanelName: p.PanelName,
	}
	rtn, err := Client.SendMessageWait(lpm)
	if err != nil {
		log.Printf("Dashborg.LookupPanel() error looking up panel err:%v\n", err)
		return err
	}
	var mrtn transport.MappingsReturn
	err = mapstructure.Decode(rtn, &mrtn)
	if err != nil {
		log.Printf("Dashborg.LookupPanel() bad return value from server err:%v\n", err)
		return err
	}
	p.ControlMappings = make(map[string]*Control)
	for _, m := range mrtn.Mappings {
		p.ControlMappings[m.ControlName] = &Control{PanelName: p.PanelName, ControlType: m.ControlType, ControlLoc: m.ControlLoc}
	}
	return nil
}

func (p *Panel) LookupControl(controlType string, controlName string) *Control {
	c := p.ControlMappings[controlName]
	if c == nil {
		return &Control{ControlType: "invalid"}
	}
	if c.ControlType != controlType {
		return &Control{ControlType: "invalid"}
	}
	rtn := &Control{PanelName: p.PanelName, ControlType: c.ControlType, ControlLoc: c.ControlLoc}
	if rtn.GetMeta().TrackActive {
		rtn.ClientId = uuid.New().String()
		Client.TrackActive(rtn.ControlType, rtn.ControlLoc, rtn.ClientId)
	}
	return rtn
}

func (p *Panel) OnRequest(handlerPath string, handlerFn func(*PanelRequest) error) {
}

type IZone interface {
	LookupControl(controlType string, controlName string) Control
}

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
}

func LogInfo(fmt string, data ...interface{}) {
	if Client.Config.Verbose {
		log.Printf(fmt, data...)
	}
}
