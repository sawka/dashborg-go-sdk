package dash

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"github.com/sawka/dashborg-go-sdk/pkg/transport"
)

const MAX_BLOB_SIZE = 10 * 1024 * 1024

type Control struct {
	ClientId    string `json:"clientid"`
	PanelName   string `json:"panelname"`
	ControlType string `json:"controltype"` // elemtype
	ControlLoc  string `json:"controlloc"`
}

func (c *Control) IsValid() bool {
	return c.ControlLoc != "" && c.ControlLoc != dashutil.INVALID_CLOC && c.ControlType != "" && c.ControlType != "invalid"
}

func (c *Control) GetControlId() string {
	cloc, err := dashutil.ParseControlLocator(c.ControlLoc)
	if err != nil {
		return ""
	}
	return cloc.ControlId
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
		PanelName:  c.PanelName,
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
	Client.RegisterPushFn(c.GetControlId(), runFn, false)
}

func (c *Control) OnCheckboxChange(fn func(b bool) error) {
	if c.ControlType != "input" || !c.IsValid() {
		return
	}
	runFn := func(v interface{}) (interface{}, error) {
		err := fn(v.(bool))
		if err != nil {
			return nil, err
		}
		return true, nil
	}
	Client.RegisterPushFn(c.GetControlId(), runFn, false)
}

func (c *Control) OnSelectChange(fn func(v string) error) {
	if c.ControlType != "inputselect" || !c.IsValid() {
		return
	}
	runFn := func(v interface{}) (interface{}, error) {
		err := fn(v.(string))
		if err != nil {
			return nil, err
		}
		return true, nil
	}
	fmt.Printf("register pushfn %s\n", c.ControlLoc)
	Client.RegisterPushFn(c.GetControlId(), runFn, false)
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

func (c *Control) LogControl(text string, args ...BuilderArg) *Control {
	if c.ControlType != "log" || !c.IsValid() {
		return nil
	}
	b := c.ElemBuilder()
	b.NoImplicitRoot = true
	rtn := b.Print(text, args...)
	b.Flush()
	return rtn
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
		PanelName:  c.PanelName,
		Data:       rdata,
	}
	Client.SendMessage(m)
}

func (c *Control) DynSetFStr(fstr string) {
	if c.ControlType != "dyn" || !c.IsValid() {
		return
	}
	dynData := transport.DynElemData{}
	if fstr == "" {
		dynData.ClearFStr = true
	} else {
		dynData.FStr = fstr
	}
	m := transport.ControlUpdateMessage{
		MType:      "controlupdate",
		Cmd:        "setdata",
		Ts:         Ts(),
		ControlLoc: c.ControlLoc,
		PanelName:  c.PanelName,
		Data:       dynData,
	}
	Client.SendMessage(m)
}

func (c *Control) DynSetData(data ...interface{}) {
	if c.ControlType != "dyn" || !c.IsValid() {
		return
	}
	dynData := transport.DynElemData{}
	if len(data) == 0 {
		dynData.ClearData = true
	} else {
		dynData.Data = data
	}
	m := transport.ControlUpdateMessage{
		MType:      "controlupdate",
		Cmd:        "setdata",
		Ts:         Ts(),
		ControlLoc: c.ControlLoc,
		PanelName:  c.PanelName,
		Data:       dynData,
	}
	Client.SendMessage(m)
}

func (c *Control) ElemBuilder() *EmbedControlWriter {
	if (c.ControlType != "dyn" && c.ControlType != "table" && c.ControlType != "log") || !c.IsValid() {
		log.Printf("Invalid Control for creating an ElemBuilder.  Must be a valid dyn, table, or log control.")
	}
	return makeEmbedControlWriter(c)
}

func (c *Control) HandlerOnAllRequests(fn func(req *PanelRequest) (bool, error)) {
	runFn := func(v interface{}) (interface{}, error) {
		var preqData transport.PanelRequestData
		err := mapstructure.Decode(v, &preqData)
		if err != nil {
			return nil, fmt.Errorf("Cannot decode PanelRequestData err:%w", err)
		}
		req := &PanelRequest{
			ZoneName:    Client.Config.ZoneName,
			PanelName:   c.PanelName,
			FeClientId:  preqData.FeClientId,
			ReqId:       uuid.New().String(),
			HandlerPath: preqData.Handler,
			Data:        preqData.Data,
			Depth:       preqData.Depth,
		}
		ok, err := fn(req)
		if err != nil {
			return nil, err
		}
		if ok {
			return true, nil
		}
		return nil, nil
	}
	Client.RegisterPushFn(c.GetControlId(), runFn, false)
}

func (c *Control) TableAddRow(rowStr string, args ...BuilderArg) {
	if c.ControlType != "table" || !c.IsValid() {
		log.Printf("Invalid table control to call TableAddRow")
		return
	}
	b := c.ElemBuilder()
	b.Print(rowStr, args...)
	b.Flush()
}

func (c *Control) ImageSetBlobHash(blobHash string) {
	if c.ControlType != "image" || !c.IsValid() {
		log.Printf("SetImageBlobHash is only supported on valid image controls\n")
		return
	}
	updateMsg := transport.ControlUpdateMessage{
		MType:      "controlupdate",
		Ts:         Ts(),
		ControlLoc: c.ControlLoc,
		PanelName:  c.PanelName,
		Cmd:        "setblob",
		Data:       blobHash,
	}
	Client.SendMessage(updateMsg)
}

func (c *Control) ImageUploadBlob(mimeType string, r io.ReaderAt) error {
	if c.ControlType != "image" || !strings.HasPrefix(mimeType, "image/") || !c.IsValid() {
		return fmt.Errorf("UploadBlob is only supported on valid image controls, with image/* mime-types")
	}
	blobHash, err := UploadBlob(mimeType, r)
	if err != nil {
		return err
	}
	c.ImageSetBlobHash(blobHash)
	return nil
}

func sha256FromReader(r io.ReaderAt) (string, int64, error) {
	hash := sha256.New()
	var pos int64
	var bufArr [64 * 1024]byte
	buf := bufArr[:]
	for {
		n, err := r.ReadAt(buf, pos)
		if n > 0 {
			_, err = hash.Write(buf[:n])
			if err != nil {
				return "", 0, err
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
		pos += int64(n)
	}
	hashVal := hash.Sum(nil)
	return fmt.Sprintf("%x", hashVal), pos, nil
}
