package dash

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/bufsrv"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"github.com/sawka/dashborg-go-sdk/pkg/transport"
)

const (
	SUBELEM_TEXT = "T"
	SUBELEM_ONE  = "1"
	SUBELEM_LIST = "*"
)

var handlerRe *regexp.Regexp = regexp.MustCompile("^(/[a-zA-Z0-9_-]+)/")

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
	CMeta["table"] = makeCTM("embed subctl sub-*")
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
	rtn := &ContextWriter{}
	p, _ := LookupPanel(req.PanelName) // TODO maybe PanelRequest should return context names?
	ctx := p.LookupControl("context", name)
	var ctxId string
	if ctx.IsValid() {
		cloc, err := dashutil.ParseControlLocator(ctx.ControlLoc)
		if err != nil {
			ctxId = cloc.ControlId
		}
	}
	if ctxId == "" {
		ctxId = uuid.New().String()
	}
	rtn.ElemBuilder = MakeElemBuilder(dashutil.MakeEphCtxLocId(req.FeClientId, ctxId, req.ReqId))
	rtn.Req = req
	rtn.ContextControl = ctx
	return rtn
}

func (req *PanelRequest) GetHandlerPath() string {
	return req.HandlerPath
}

func (req *PanelRequest) GetData() interface{} {
	return req.Data
}

func (p *PanelRequest) TriggerRequest(handlerPath string, data interface{}) {
	if p.Depth > 5 {
		log.Printf("Dashborg Cannot trigger requests more than 5 levels deep\n")
		return
	}
	req := &transport.PanelRequestData{
		FeClientId: p.FeClientId,
		ZoneName:   p.ZoneName,
		PanelName:  p.PanelName,
		Handler:    handlerPath,
		Data:       data,
		Depth:      p.Depth + 1,
	}
	handlerMatch := handlerRe.FindStringSubmatch(handlerPath)
	if handlerMatch == nil {
		log.Printf("Dashborg Bad handlerPath:%s passed to TriggerRequest\n", handlerPath)
		return
	}
	panel, _ := LookupPanel(p.PanelName)
	// TODO don't activate this control
	handlerControl := panel.LookupControl("handler", handlerMatch[1])
	if !handlerControl.IsValid() {
		log.Printf("Dashborg, cannot find handler control for %s\n", handlerMatch[1])
		return
	}
	go func() {
		Client.handlePush(bufsrv.PushType{
			PushCtx:     Client.GetProcRunId(),
			PushRecvId:  handlerControl.ControlLoc,
			PushPayload: req,
		})
	}()
}

type ContextWriter struct {
	*ElemBuilder
	Req            *PanelRequest
	ContextControl *Control
}

func ComputeElemHash(elemText []string) string {
	h := md5.New()
	for _, text := range elemText {
		io.WriteString(h, text)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (w *ContextWriter) Flush() {
	if !w.ContextControl.IsValid() {
		log.Printf("Dashborg attempt to Flush() an invalid ContextControl\n")
		return
	}
	elem := w.DoneElem()
	w.ReportErrors(os.Stderr)
	elemText := elem.ElemTextEx(0, nil)
	// fmt.Printf("context writer flush %v\n", w.ContextControl)
	// fmt.Printf("%s\n", strings.Join(elemText, "\n"))
	m := transport.WriteContextMessage{
		MType:      "writecontext",
		Ts:         Ts(),
		ZoneName:   Client.Config.ZoneName,
		PanelName:  w.ContextControl.PanelName,
		ContextLoc: w.ContextControl.ControlLoc,
		FeClientId: w.Req.FeClientId,
		ReqId:      w.Req.ReqId,
		ElemText:   elemText,
	}
	Client.SendMessage(m)
}

func (w *ContextWriter) Revert() {
	if !w.ContextControl.IsValid() {
		log.Printf("Dashborg attempt to Revert() an invalid ContextControl\n")
		return
	}
	fmt.Printf("context writer revert %v\n", w.ContextControl)
	m := transport.WriteContextMessage{
		MType:      "writecontext",
		Ts:         Ts(),
		ZoneName:   Client.Config.ZoneName,
		PanelName:  w.ContextControl.PanelName,
		ContextLoc: w.ContextControl.ControlLoc,
		FeClientId: w.Req.FeClientId,
		ReqId:      w.Req.ReqId,
		ElemText:   nil,
	}
	Client.SendMessage(m)
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

func DefinePanelFromFile(panelName string, fileName string) (*Panel, error) {
	rtnPanel := &Panel{PanelName: panelName}
	fd, err := os.Open(fileName)
	if err != nil {
		return rtnPanel, err
	}
	defer fd.Close()
	pw := DefinePanel(panelName)
	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		pw.Print(scanner.Text())
	}
	err = scanner.Err()
	if err != nil {
		log.Printf("Dashborg.DefinePanelFromFile error reading file:%s err:%v\n", fileName, err)
		return rtnPanel, err
	}
	return pw.Flush()
}

func (p *PanelWriter) Flush() (*Panel, error) {
	elemText := p.DoneText()
	m := transport.DefinePanelMessage{
		MType:     "definepanel",
		Ts:        Ts(),
		ZoneName:  Client.Config.ZoneName,
		PanelName: p.PanelName,
		TrackAnon: !p.ElemBuilder.NoAnon,
		ElemText:  elemText,
		ElemHash:  ComputeElemHash(elemText),
	}
	fmt.Printf("elemhash: %v\n", m.ElemHash)
	p.ElemBuilder.ReportErrors(os.Stderr)
	rtn, err := Client.SendMessageWait(m)
	rtnPanel := &Panel{PanelName: p.PanelName}
	fmt.Printf("DefinePanel err:%v rtn:%v\n", err, rtn)
	if err != nil {
		return rtnPanel, err
	}
	err = rtnPanel.setControlMappings(rtn)
	return rtnPanel, err
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

func (p *Panel) setControlMappings(rtn interface{}) error {
	var mrtn transport.MappingsReturn
	err := mapstructure.Decode(rtn, &mrtn)
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
	err = p.setControlMappings(rtn)
	return err
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
	handlerMatch := handlerRe.FindStringSubmatch(handlerPath)
	if handlerMatch == nil {
		log.Printf("Bad handlerPath:%s passed to OnRequest", handlerPath)
		return
	}
	handlerControl := p.LookupControl("handler", handlerMatch[1])
	fmt.Printf("OnRequest lookup handler:%s rtn:%v\n", handlerMatch[1], handlerControl)
	handlerControl.OnAllRequests(func(req *PanelRequest) (bool, error) {
		if req.GetHandlerPath() != handlerPath {
			return false, nil
		}
		err := handlerFn(req)
		if err != nil {
			return true, err
		}
		return true, nil
	})
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
