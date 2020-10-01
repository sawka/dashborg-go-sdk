package transport

import "reflect"

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

type DoneMessage struct {
	MType string `json:"mtype"`
	Ts    int64  `json:"ts"`
}

// "definepanel"
type DefinePanelMessage struct {
	MType     string   `json:"mtype"`
	Ts        int64    `json:"ts"`
	ZoneName  string   `json:"zonename"`
	PanelName string   `json:"panelname"`
	ElemText  []string `json:"elemtext"`
}

// "lookuppanel"
type LookupPanelMessage struct {
	MType     string `json:"mtype"`
	Ts        int64  `json:"ts"`
	ZoneName  string `json:"zonename"`
	PanelName string `json:"panelname"`
}

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

type ControlUpdateMessage struct {
	MType      string      `json:"mtype"`
	Ts         int64       `json:"ts"`
	ControlLoc string      `json:"controlloc"`
	Cmd        string      `json:"cmd"`
	Data       interface{} `json:"data"`
}

type ControlAppendMessage struct {
	MType      string      `json:"mtype"`
	Ts         int64       `json:"ts"`
	PanelName  string      `json:"panelname"`
	ControlLoc string      `json:"controlloc"`
	ExpSec     int64       `json:"expsec,omitempty"` // seconds to expire data
	Data       interface{} `json:"data"`
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
}
