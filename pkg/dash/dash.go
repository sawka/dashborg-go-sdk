//
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
	CMeta["dyn"] = makeCTM("inline embed control hasdata sub-1")
	CMeta["image"] = makeCTM("inline embed control hasdata")
	CMeta["log"] = makeCTM("rowdata control hasdata active subctl")
	CMeta["button"] = makeCTM("inline embed control active sub-1")
	CMeta["context"] = makeCTM("control sub-* eph")
	CMeta["progress"] = makeCTM("inline embed control active hasdata")
	CMeta["handler"] = makeCTM("control active")
	CMeta["input"] = makeCTM("inline embed control")
	CMeta["inputselect"] = makeCTM("inline embed control rowdata sub-*")
	CMeta["option"] = makeCTM("embed sub-T")
	CMeta["table"] = makeCTM("embed control hasdata rowdata subctl sub-*")
	CMeta["th"] = makeCTM("sub-*")
	CMeta["tdformat"] = makeCTM("sub-*")

	CMeta["context"].AllowedSubTypes = map[string]bool{"context": true, "modal": true}
	CMeta["dyn"].AllowedSubTypes = map[string]bool{"text": true}
	CMeta["progress"].AllowedSubTypes = map[string]bool{"bar": true, "spinner": true}
	CMeta["input"].AllowedSubTypes = map[string]bool{
		"text":     true,
		"password": true,
		"hidden":   true,
		"checkbox": true,
		"toggle":   true,
		"date":     true,
		"time":     true,
		"datetime": true,
	}
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
	// DASHBORG_ACCID, set to force an AccountId (must match certificate).  If not set, AccountId is set from certificate file.
	// If AccId is given and AutoKeygen is true, and key/cert files are not found, Dashborg will create a new self-signed
	//     keypair using the AccId given.
	// If AccId is given, and the certificate does not match, this will cause a panic.
	AccId string

	// Set to true for unregistered accounts
	AnonAcc bool

	// DASHBORG_ZONE defaults to "default"
	ZoneName string

	// Process Name Attributes.  Only ProcName is required
	ProcName  string // DASHBORG_PROCNAME (set from executable filename if not set)
	ProcIName string
	ProcTags  map[string]string

	KeyFileName  string // DASHBORG_KEYFILE private key file
	CertFileName string // DASHBORG_CERTFILE certificate file, CN must be set to your Dashborg Account Id.

	// Create a self-signed key/cert if they do not exist.  This will also create a random Account Id.
	// Should only be used with AnonAcc is true, and AccId is not set
	AutoKeygen bool

	// The minimum amount of time to wait for all events to complete processing before shutting down after calling WaitForClear()
	// Defaults to 1 second.
	MinClearTimeout time.Duration

	// DASHBORG_PANELCACHEMS, sets how long LookupPanel calls are cached
	// The environment variable sets the time in milliseconds.
	// In order to disable the cache (not recommended), set the environment variable to "0" or
	//   set the value in the Config structure to <= 1ms.
	PanelCacheTime time.Duration

	// DASHBORG_VERBOSE, set to true for extra debugging information
	Verbose bool

	// These are for internal testing, should not normally be set by clients.
	Env        string // DASHBORG_ENV
	BufSrvHost string // DASHBORG_PROCHOST
	BufSrvPort int    // DASHBORG_PROCPORT
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
	Data        interface{}
	Depth       int
	HandlerPath string
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
	rtn.ElemBuilder = MakeElemBuilder(req.PanelName, dashutil.MakeEphCtxLocId(req.FeClientId, ctxId, req.ReqId), 0)
	rtn.FeClientId = req.FeClientId
	rtn.ReqId = req.ReqId
	rtn.ContextControl = ctx
	return rtn
}

func (req *PanelRequest) GetHandlerPath() string {
	return req.HandlerPath
}

func (req *PanelRequest) GetData() interface{} {
	return req.Data
}

func (req *PanelRequest) TriggerRequest(handlerPath string, data interface{}) {
	if req.Depth > 5 {
		log.Printf("Dashborg Cannot trigger requests more than 5 levels deep\n")
		return
	}
	reqData := &transport.PanelRequestData{
		FeClientId: req.FeClientId,
		ZoneName:   req.ZoneName,
		PanelName:  req.PanelName,
		Handler:    handlerPath,
		Data:       data,
		Depth:      req.Depth + 1,
	}
	handlerMatch := handlerRe.FindStringSubmatch(handlerPath)
	if handlerMatch == nil {
		log.Printf("Dashborg Bad handlerPath:%s passed to TriggerRequest\n", handlerPath)
		return
	}
	panel, _ := LookupPanel(req.PanelName)
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
			PushPayload: reqData,
		})
	}()
}

type EmbedControlWriter struct {
	*ElemBuilder
	Control        *Control
	Ts             int64
	NoImplicitRoot bool
}

func makeEmbedControlWriter(c *Control) *EmbedControlWriter {
	rtn := &EmbedControlWriter{}
	cloc, err := dashutil.ParseControlLocator(c.ControlLoc)
	var locId string
	if err == nil {
		locId = dashutil.MakeScLocId(cloc.IsEph(), cloc.ControlId)
	}
	rtn.Ts = Ts()
	rtn.ElemBuilder = MakeElemBuilder(c.PanelName, locId, rtn.Ts)
	rtn.Control = c
	return rtn
}

func (w *EmbedControlWriter) Flush() {
	c := w.Control
	if c == nil || (c.ControlType != "dyn" && c.ControlType != "table" && c.ControlType != "log") || !c.IsValid() {
		log.Printf("Invalid Control for creating an ElemBuilder, cannot Flush().  Must be a valid dyn, table, or log control.")
		return
	}
	elem := w.DoneElem()
	if c.ControlType == "dyn" {
		if w.ElemBuilder.ImplicitRoot && len(elem.List) == 1 {
			elem = elem.List[0]
		}
	} else if w.NoImplicitRoot && w.ElemBuilder.ImplicitRoot {
		if len(elem.List) == 0 {
			return
		}
		elem = elem.List[0]
	}
	fmt.Printf("EmbedControlWriter %s\n", c.ControlType)
	elem.Dump(os.Stdout)
	w.ReportErrors(os.Stderr)
	elemText := elem.ElemTextEx(0, nil)
	if c.ControlType == "log" || c.ControlType == "table" {
		entry := transport.LogEntry{
			Ts:        w.Ts,
			ProcRunId: Client.GetProcRunId(),
			ElemText:  elemText,
		}
		m := transport.ControlAppendMessage{
			MType:      "controlappend",
			Ts:         w.Ts,
			PanelName:  c.PanelName,
			ControlLoc: c.ControlLoc,
			Data:       entry,
		}
		Client.SendMessage(m)
	} else if c.ControlType == "dyn" {
		dynData := transport.DynElemData{
			Ts:        w.Ts,
			ProcRunId: Client.GetProcRunId(),
			ElemText:  elemText,
		}
		m := transport.ControlUpdateMessage{
			MType:      "controlupdate",
			Cmd:        "setdata",
			Ts:         w.Ts,
			ControlLoc: c.ControlLoc,
			PanelName:  c.PanelName,
			Data:       dynData,
		}
		fmt.Printf("sending dyn message panel:%s %#v\n", c.PanelName, c)
		Client.SendMessage(m)
	}
}

type ContextWriter struct {
	*ElemBuilder
	FeClientId     string
	ReqId          string
	ContextControl *Control
}

func computeElemHash(elemText []string) string {
	h := md5.New()
	for _, text := range elemText {
		io.WriteString(h, text)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (req *PanelRequest) SendFile(fileName string, mimeType string, fd io.ReaderAt) error {
	hash, err := UploadBlob(mimeType, fd)
	if err != nil {
		return err
	}
	sfm := transport.SendFileMessage{
		MType:      "sendfile",
		MimeType:   mimeType,
		BlobHash:   hash,
		FileName:   fileName,
		PanelName:  req.PanelName,
		FeClientId: req.FeClientId,
		ReqId:      req.ReqId,
	}
	Client.SendMessage(sfm)
	return nil
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
		FeClientId: w.FeClientId,
		ReqId:      w.ReqId,
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
		FeClientId: w.FeClientId,
		ReqId:      w.ReqId,
		ElemText:   nil,
	}
	Client.SendMessage(m)
}

type PanelWriter struct {
	*ElemBuilder
	PanelName string
}

func ParseElemText(panelName string, elemText []string, locId string, controlTs int64, anonProcRunId string) *Elem {
	// never allow implicit root when parsing (already done on proc side)
	if len(elemText) == 0 {
		return nil
	}
	b := MakeElemBuilder(panelName, locId, controlTs)
	for _, text := range elemText {
		b.Print(text)
	}
	elem := b.DoneElem()
	if b.ImplicitRoot && len(elem.List) == 1 {
		elem = elem.List[0]
	}
	if elem != nil && anonProcRunId != "" {
		elem.Walk(func(e *Elem) {
			meta := e.GetMeta()
			if meta.HasControl && e.ControlName == "" && e.ControlLoc != "" {
				e.AnonProcRunId = anonProcRunId
			}
		})
	}
	return elem
}

func DefinePanel(panelName string) *PanelWriter {
	rtn := &PanelWriter{PanelName: panelName}
	rtn.ElemBuilder = MakeElemBuilder(panelName, dashutil.MakeZPLocId(Client.Config.ZoneName, panelName), 0)
	rtn.ElemBuilder.SetRootDivClass("rootdiv")
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
		ElemHash:  computeElemHash(elemText),
	}
	p.ElemBuilder.ReportErrors(os.Stderr)
	rtn, err := Client.SendMessageWait(m)
	rtnPanel := &Panel{PanelName: p.PanelName}
	if err != nil {
		return rtnPanel, err
	}
	err = rtnPanel.setControlMappings(rtn)
	if err == nil {
		setMappingsInCache(p.PanelName, rtnPanel.ControlMappings)
	}
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
	mappings, ok := getMappingsFromCache(panelName, Client.Config.PanelCacheTime)
	if ok {
		p.ControlMappings = mappings
		return p, nil
	}
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

// force a refresh, bypasses cache
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
	if err == nil {
		setMappingsInCache(p.PanelName, p.ControlMappings)
	}
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
	handlerControl.HandlerOnAllRequests(func(req *PanelRequest) (bool, error) {
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

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
}

func logInfo(fmt string, data ...interface{}) {
	if Client.Config.Verbose {
		log.Printf(fmt, data...)
	}
}

// returns (blobHash, err)
func UploadBlob(mimeType string, r io.ReaderAt) (string, error) {
	if !dashutil.IsMimeTypeValid(mimeType) {
		return "", fmt.Errorf("Invalid mime-type passed to UploadBlob mime-type:%s", mimeType)
	}
	hashVal, size, err := sha256FromReader(r)
	if err != nil {
		return "", err
	}
	if size > MAX_BLOB_SIZE {
		return "", fmt.Errorf("Maximum Blob size exceeded max:%d size:%d\n", MAX_BLOB_SIZE, size)
	}
	checkBlobMsg := transport.CheckBlobMessage{
		MType:    "checkblob",
		BlobHash: hashVal,
	}
	checkRtn, err := Client.SendMessageWait(checkBlobMsg)
	if err != nil {
		return "", fmt.Errorf("Error from CheckBlobMessage err:%w", err)
	}
	if checkBool, ok := checkRtn.(bool); ok && checkBool {
		return hashVal, nil
	}
	// format = SHA-265 + len(mimeType) + mimeType + data
	buf := make([]byte, 64+1+len(mimeType)+int(size))
	copy(buf[0:], []byte(hashVal))   // 64
	buf[64] = byte(len(mimeType))    // 1
	copy(buf[65:], []byte(mimeType)) // len(mimeType)
	n, err := r.ReadAt(buf[65+len(mimeType):], 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	if int64(n) != size {
		// this shouldn't happen
		return "", fmt.Errorf("Could not read blob data fully (partial read, invalid ReaderAt)")
	}
	rawMsg := transport.RawMessage{
		MType: "raw:blob",
		Data:  buf,
	}
	Client.SendMessage(rawMsg)
	return hashVal, nil
}
