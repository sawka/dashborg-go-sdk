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

type DefinePanelMessage struct {
	MType     string   `json:"mtype"`
	Ts        int64    `json:"ts"`
	ZoneName  string   `json:"zonename"`
	PanelName string   `json:"panelname"`
	ElemText  []string `json:"elemtext"`
}

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
	ExpSec     int64       `json:"expsec"` // seconds to expire data
	Data       interface{} `json:"data"`

	ClientId string `json:"-"` // for client side
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
