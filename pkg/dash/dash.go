package dash

import (
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
)

type Config struct {
	// DASHBORG_ACCID, set to force an AccountId (must match certificate).  If not set, AccountId is set from certificate file.
	// If AccId is given and AutoKeygen is true, and key/cert files are not found, Dashborg will create a new self-signed
	//     keypair using the AccId given.
	// If AccId is given, and the certificate does not match, this will cause a panic.
	AccId string

	// Set to true for unregistered accounts
	AnonAcc bool

	// Set to enable LocalServer mode
	LocalServer bool
	LocalClient dashproto.DashborgServiceClient

	// DASHBORG_ZONE defaults to "default"
	ZoneName string

	// Process Name Attributes.  Only ProcName is required
	ProcName string // DASHBORG_PROCNAME (set from executable filename if not set)
	ProcTags map[string]string

	KeyFileName  string // DASHBORG_KEYFILE private key file (defaults to dashborg-client.key)
	CertFileName string // DASHBORG_CERTFILE certificate file, CN must be set to your Dashborg Account Id.  (defaults to dashborg-client.crt)

	// Create a self-signed key/cert if they do not exist.  This will also create a random Account Id.
	// Should only be used with AnonAcc is true.  If AccId is set, will create a key with that AccId
	AutoKeygen bool

	// Set to true to allow other backends in the same zone to call data functions using dash.CallDataHandler
	AllowBackendCalls bool

	// The minimum amount of time to wait for all events to complete processing before shutting down after calling WaitForClear()
	// Defaults to 1 second.
	MinClearTimeout time.Duration

	// DASHBORG_VERBOSE, set to true for extra debugging information
	Verbose bool

	// These are for internal testing, should not normally be set by clients.
	Env             string // DASHBORG_ENV
	DashborgSrvHost string // DASHBORG_PROCHOST
	DashborgSrvPort int    // DASHBORG_PROCPORT

	setupDone bool // internal
}

type AuthAtom struct {
	Scope string      `json:"scope"` // scope of this atom app or zone
	Type  string      `json:"type"`  // auth type (password, noauth, dashborg, deauth, or user-defined)
	Ts    int64       `json:"ts"`    // expiration Ts (ms) of this auth atom
	Role  string      `json:"role"`
	Id    string      `json:"id,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

type AllowedAuth interface {
	checkAuth(*PanelRequest) (bool, error) // should call setAuthData if returns true
}

type ReflectZoneType struct {
	AccId    string                      `json:"accid"`
	ZoneName string                      `json:"zonename"`
	Procs    map[string]ReflectProcType  `json:"procs"`
	Panels   map[string]ReflectPanelType `json:"panels"`
	Apps     map[string]ReflectAppType   `json:"apps"`
}

type ReflectPanelType struct {
	PanelName     string                        `json:"panelname"`
	PanelHandlers map[string]ReflectHandlerType `json:"panelhandlers"`
	DataHandlers  map[string]ReflectHandlerType `json:"datahandlers"`
}

type ReflectAppType struct {
	AppName    string   `json:"appname"`
	ProcRunIds []string `json:"procrunids"`
}

type ReflectProcType struct {
	StartTs   int64             `json:"startts"`
	ProcName  string            `json:"procname"`
	ProcTags  map[string]string `json:"proctags"`
	ProcRunId string            `json:"procrunid"`
}

type ReflectHandlerType struct {
	ProcRunIds []string `json:"procrunids"`
}

type StreamOpts struct {
	StreamId       string `json:"streamid"`       // if unset will be set to a random uuid
	ControlPath    string `json:"controlpath"`    // control path for client cancelation
	NoServerCancel bool   `json:"noservercancel"` // set to true to keep running the stream, even when there are no clients listening (or on server error)
}
