package dash

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
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

func panelLink(panelName string) string {
	accId := globalClient.Config.AccId
	zoneName := globalClient.Config.ZoneName
	if globalClient.Config.Env != "prod" {
		return fmt.Sprintf("https://acc-%s.console.dashborg-dev.com:8080/zone/%s/%s", accId, zoneName, panelName)
	}
	return fmt.Sprintf("https://acc-%s.console.dashborg.net/zone/%s/%s", accId, zoneName, panelName)
}

type StreamOpts struct {
	StreamId       string `json:"streamid"`       // if unset will be set to a random uuid
	ControlPath    string `json:"controlpath"`    // control path for client cancelation
	NoServerCancel bool   `json:"noservercancel"` // set to true to keep running the stream, even when there are no clients listening (or on server error)
}

// Bare streams start with no connected clients.  ControlPath is ignored, and NoServerCancel must be set to true.
// A future request can attach to the stream by calling req.StartStream() and passing the
// same StreamId.  An error will be returned if a stream with this StreamId has already started.
// Unlike StartStream StreamId must be specified ("" will return an error).
// Caller is responsible for calling req.Done() when the stream is finished.
func StartBareStream(panelName string, streamOpts StreamOpts) (*PanelRequest, error) {
	if !streamOpts.NoServerCancel {
		return nil, fmt.Errorf("BareStreams must have NoServerCancel set in StreamOpts")
	}
	if !dashutil.IsTagValid(streamOpts.StreamId) {
		return nil, fmt.Errorf("Invalid StreamId")
	}
	streamReqId, ctx, err := globalClient.startBareStream(panelName, streamOpts)
	if err != nil {
		return nil, err
	}
	streamReq := &PanelRequest{
		StartTime:   time.Now(),
		ctx:         ctx,
		lock:        &sync.Mutex{},
		PanelName:   panelName,
		ReqId:       streamReqId,
		RequestType: "stream",
		Path:        streamOpts.StreamId,
	}
	return streamReq, nil
}

// RegisterPanelHandler registers a panel handler.  All panels require a root handler (path = "/").
func RegisterPanelHandler(panelName string, path string, handlerFn func(*PanelRequest) error) {
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "handler",
		Path:        path,
	}
	hfn := func(req *PanelRequest) (interface{}, error) {
		err := handlerFn(req)
		return nil, err
	}
	globalClient.registerHandler(hkey, hfn)
	if path == "/" && !globalClient.Config.LocalServer {
		log.Printf("Dashborg Panel Link [%s]: %s\n", panelName, panelLink(panelName))
	}
}

// RegisterDataHandler registers a data handler.
func RegisterDataHandler(panelName string, path string, handlerFn func(*PanelRequest) (interface{}, error)) {
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "data",
		Path:        path,
	}
	globalClient.registerHandler(hkey, handlerFn)
}

func logV(fmtStr string, args ...interface{}) {
	if globalClient != nil && globalClient.Config.Verbose {
		log.Printf(fmtStr, args...)
	}
}

func CallDataHandler(panelName string, path string, data interface{}) (interface{}, error) {
	jsonData, err := dashutil.MarshalJson(data)
	if err != nil {
		return nil, err
	}
	m := &dashproto.CallDataHandlerMessage{
		Ts:        dashutil.Ts(),
		PanelName: panelName,
		Path:      path,
		JsonData:  jsonData,
	}
	resp, err := globalClient.DBService.CallDataHandler(globalClient.ctxWithMd(), m)
	if err != nil {
		return nil, err
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}
	if !resp.Success {
		return nil, errors.New("Error calling CallDataHandler()")
	}
	var rtn interface{}
	if resp.JsonData != "" {
		err = json.Unmarshal([]byte(resp.JsonData), &rtn)
		if err != nil {
			return nil, err
		}
	}
	return rtn, nil
}

func BackendPush(panelName string, path string) error {
	m := &dashproto.BackendPushMessage{
		Ts:        dashutil.Ts(),
		PanelName: panelName,
		Path:      path,
	}
	return globalClient.backendPush(m)
}

func ReflectZone() (*ReflectZoneType, error) {
	m := &dashproto.ReflectZoneMessage{Ts: dashutil.Ts()}
	resp, err := globalClient.DBService.ReflectZone(globalClient.ctxWithMd(), m)
	if err != nil {
		return nil, err
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}
	if !resp.Success {
		return nil, errors.New("Error calling ReflectZone()")
	}
	var rtn ReflectZoneType
	err = json.Unmarshal([]byte(resp.JsonData), &rtn)
	if err != nil {
		return nil, err
	}
	return &rtn, nil
}

func ConnectApp(app AppRuntime) error {
	m, err := makeAppMessage(app)
	if err != nil {
		return err
	}
	resp, err := globalClient.DBService.ConnectApp(globalClient.ctxWithMd(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return errors.New("Error calling ConnectApp()")
	}
	globalClient.connectApp(app)
	for name, warning := range resp.OptionWarnings {
		log.Printf("ConnectApp WARNING option[%s]: %s\n", name, warning)
	}
	return nil
}
