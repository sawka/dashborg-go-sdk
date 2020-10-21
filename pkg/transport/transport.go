package transport

import "reflect"

// proc
type ProcMessage struct {
	MType          string            `json:"mtype"`
	Ts             int64             `json:"ts"`
	AccId          string            `json:"accid"`
	AnonAcc        bool              `json:"anonacc"`
	ProcName       string            `json:"procname"`
	ProcIName      string            `json:"prociname"`
	ProcTags       map[string]string `json:"proctags"`
	ProcRunId      string            `json:"procrunid"`
	ZoneName       string            `json:"zonename"`
	StartTs        int64             `json:"startts"`
	HostData       map[string]string `json:"hostdata"`
	ActiveControls []string          `json:"activecontrols"`

	Pk256 string `json:"-"` // used on server side
}

// raw message will not be marshaled into json
// raw:blob =>
//   64-bytes | SHA-256
//   1-byte   | len(mimeType)
//   n-bytes  | mimeType
//   rest     | blob value
// raw:sendfile =>
//   1-byte   | len(mimeType)
//   n-bytes  | mimeType
//   rest     | blob value
type RawMessage struct {
	MType string `json:"mtype"`
	Data  []byte `json:"data"`
}

// done
type DoneMessage struct {
	MType string `json:"mtype"`
	Ts    int64  `json:"ts"`
}

// keepalive
type KeepAliveMessage struct {
	MType string `json:"mtype"`
}

// definepanel
type DefinePanelMessage struct {
	MType     string   `json:"mtype"`
	Ts        int64    `json:"ts"`
	ZoneName  string   `json:"zonename"`
	PanelName string   `json:"panelname"`
	TrackAnon bool     `json:"trackanon"`
	ElemText  []string `json:"elemtext"`
	ElemHash  string   `json:"elemhash"`
}

// lookuppanel
type LookupPanelMessage struct {
	MType     string `json:"mtype"`
	Ts        int64  `json:"ts"`
	ZoneName  string `json:"zonename"`
	PanelName string `json:"panelname"`
}

// writecontext
type WriteContextMessage struct {
	MType      string   `json:"mtype"`
	Ts         int64    `json:"ts"`
	ZoneName   string   `json:"zonename"`
	PanelName  string   `json:"panelname"`
	ContextLoc string   `json:"contextloc"` // ControlLoc for context to write
	FeClientId string   `json:"feclientid"`
	ReqId      string   `json:"reqid"`
	ElemText   []string `json:"elemtext"`
}

type ResetTsData struct {
	ResetTs int64 `json:"resetts"`
}

// checkblob
type CheckBlobMessage struct {
	MType    string `json:"mtype"`
	BlobHash string `json:"blobhash"`
}

// sendfile
type SendFileMessage struct {
	MType      string `json:"mtype"`
	FileName   string `json:"filename"`
	MimeType   string `json:"mimetype"`
	BlobHash   string `json:"blobhash"`
	PanelName  string `json:"panelname"`
	FeClientId string `json:"feclientid"`
	ReqId      string `json:"reqid"`
}

// controlupdate
type ControlUpdateMessage struct {
	MType      string      `json:"mtype"`
	Ts         int64       `json:"ts"`
	ControlLoc string      `json:"controlloc"`
	PanelName  string      `json:"panelname"`
	Cmd        string      `json:"cmd"`
	Data       interface{} `json:"data"`
}

// controlappend
type ControlAppendMessage struct {
	MType      string      `json:"mtype"`
	Ts         int64       `json:"ts"`
	PanelName  string      `json:"panelname"`
	ControlLoc string      `json:"controlloc"`
	ExpSec     int64       `json:"expsec,omitempty"` // seconds to expire data
	Data       interface{} `json:"data"`
}

// "activecontrols"
type ActiveControlsMessage struct {
	MType      string   `json:"mtype"`
	Ts         int64    `json:"ts"`
	Activate   []string `json:"activate"`
	Deactivate []string `json:"deactivate"`
}

type ControlMapping struct {
	ControlName string `json:"controlname"`
	ControlType string `json:"controltype"`
	ControlLoc  string `json:"controlloc"`
}

type MappingsReturn struct {
	ZoneName  string
	PanelName string
	Mappings  []ControlMapping `json:"mappings"`
}

func GetMType(m interface{}) string {
	v := reflect.ValueOf(m)
	return v.FieldByName("MType").String()
}

type LogEntry struct {
	Ts        int64       `json:"ts"`
	ProcRunId string      `json:"procrunid"`
	Text      string      `json:"text,omitempty"`
	ElemText  []string    `json:"elemtext,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Elem      interface{} `json:"elem"` // for FE communication
}

type ProgressData struct {
	DisplayMode string `json:"displaymode"` // "progress", "status", "spinner"
	Ts          int64  `json:"ts"`
	Label       string `json:"label"`
	Err         string `json:"err"`
	Max         int    `json:"max"`
	Val         int    `json:"val"`
	Inc         int    `json:"inc"`
	Status      string `json:"status"`
	ClearStatus bool   `json:"clearstatus"`
	Done        bool   `json:"done"`
	Reset       bool   `json:"reset"`
}

type PanelRequestData struct {
	FeClientId  string      `json:"feclientid"`
	ZoneName    string      `json:"zonename"`
	PanelName   string      `json:"panelname"`
	HandlerPath string      `json:"handlerpath"`
	Data        interface{} `json:"data"`
	Depth       int         `json:"depth"`
}

type TableRequestData struct {
	ControlLoc  string      `json:"controlloc"`
	FeClientId  string      `json:"feclientid"`
	ZoneName    string      `json:"zonename"`
	PanelName   string      `json:"panelname"`
	HandlerPath string      `json:"handler"`
	Data        interface{} `json:"data"`
	Depth       int         `json:"depth"`
	PageSize    int         `json:"pagesize"`
	PageNum     int         `json:"pagenum"`
	PagingData  interface{} `json:"pagingdata"`
}

type DynElemData struct {
	FStr          string            `json:"fstr"`
	Attrs         map[string]string `json:"attrs"`
	Ts            int64             `json:"ts"`
	Data          []interface{}     `json:"data"`
	ElemText      []string          `json:"elemtext"`
	ProcRunId     string            `json:"procrunid"`
	ClearFStr     bool              `json:"clearfstr"`
	ClearAttrs    bool              `json:"clearattrs"`
	ClearData     bool              `json:"cleardata"`
	ClearElemText bool              `json:"clearelemtext"`
}

type PushMessage struct {
	Message string `json:"message"`
}

// pusherror
type PushErrorMessage struct {
	MType      string `json:"mtype"`
	Message    string `json:"message"`
	StackTrace string `json:"stacktrace"`
	FeClientId string `json:"feclientid"`
}
