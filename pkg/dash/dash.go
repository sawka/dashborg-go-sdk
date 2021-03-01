package dash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

// must be divisible by 3 (for base64 encoding)
const BLOB_READ_SIZE = 3 * 340 * 1024

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
}

// PanelRequest encapsulates all the data about a Dashborg request.  Normally the only
// fields that a handler needs to access are "Data" and "PanelState" in order to read
// the parameters and UI state associated with this request.  The other fields are
// exported, but subject to change and should not be used except in advanced use cases.
type PanelRequest struct {
	StartTime      time.Time
	PanelName      string      // panel name
	ReqId          string      // unique request id
	RequestType    string      // "data", "handler", or "stream"
	Path           string      // handler or data path
	Data           interface{} // json-unmarshaled data attached to this request
	DataJson       string      // Raw JSON for Data (used for manual unmarshalling into custom struct)
	PanelState     interface{} // json-unmarshaled panel state for this request
	PanelStateJson string      // Raw JSON for PanelState (used for manual unmarshalling into custom struct)

	// The following fields are internal and subject to change.  Not for normal client usage.
	Ctx           context.Context       // gRPC context / streaming context
	FeClientId    string                // unique id for client (currently unused)
	Lock          *sync.Mutex           // synchronizes RRActions
	AuthData      []*authAtom           // authentication tokens associated with this request
	RRActions     []*dashproto.RRAction // output, these are the actions that will be returned
	Err           error                 // set if an error occured (when set, RRActions are not sent)
	IsDone        bool                  // set after Done() is called and response has been sent to server
	AuthImpl      bool                  // if not set, will default NoAuth() on Done()
	Info          []string              // debugging information
	IsStream      bool                  // true if this is a streaming request
	IsBackendCall bool                  // true if this request originated from a backend data call
}

type ZoneReflection struct {
	AccId    string                     `json:"accid"`
	ZoneName string                     `json:"zonename"`
	Procs    map[string]ProcReflection  `json:"procs"`
	Panels   map[string]PanelReflection `json:"panels"`
}

type PanelReflection struct {
	PanelName     string                       `json:"panelname"`
	PanelHandlers map[string]HandlerReflection `json:"panelhandlers"`
	DataHandlers  map[string]HandlerReflection `json:"datahandlers"`
}

type ProcReflection struct {
	StartTs   int64             `json:"startts"`
	ProcName  string            `json:"procname"`
	ProcTags  map[string]string `json:"proctags"`
	ProcRunId string            `json:"procrunid"`
}

type HandlerReflection struct {
	ProcRunIds []string `json:"procrunids"`
}

func panelLink(panelName string) string {
	accId := globalClient.Config.AccId
	zoneName := globalClient.Config.ZoneName
	if globalClient.Config.Env != "prod" {
		return fmt.Sprintf("http://console.dashborg.localdev:8080/acc/%s/%s/%s", accId, zoneName, panelName)
	}
	return fmt.Sprintf("https://console.dashborg.net/acc/%s/%s/%s", accId, zoneName, panelName)
}

func (req *PanelRequest) appendRR(rrAction *dashproto.RRAction) {
	req.Lock.Lock()
	defer req.Lock.Unlock()
	req.RRActions = append(req.RRActions, rrAction)
}

// StartStream creates a new streaming request that can send data to the original request's client.
// streamId is used to control whether a new stream will be created or if the client will attach to
// an existing stream.  The streamFn gets passed a context that is used for cancelation.
// Note that StartStream will flush any pending actions to the server.
func (req *PanelRequest) StartStream(streamId string, controlPath string, streamFn func(ctx context.Context, req *PanelRequest)) error {
	if !dashutil.IsTagValid(streamId) {
		return fmt.Errorf("Invalid StreamId")
	}
	if !dashutil.IsUUIDValid(req.FeClientId) {
		return fmt.Errorf("No FeClientId, client does not support streaming")
	}
	if req.IsDone {
		return fmt.Errorf("Cannot call StartStream(), path=%s, PanelRequest is already done", controlPath)
	}
	if req.IsStream {
		return fmt.Errorf("Cannot call StartStream(), path=%s, PanelRequest is already streaming", controlPath)
	}
	streamReqId, ctx, err := globalClient.startStream(req.PanelName, streamId, req.FeClientId)
	if err != nil {
		return err
	}
	jsonData, _ := marshalJson(map[string]interface{}{"reqid": streamReqId})
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "streamopen",
		Selector:   controlPath,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	req.Flush() // TODO flush error
	if ctx != nil {
		streamReq := &PanelRequest{
			StartTime:   time.Now(),
			Ctx:         ctx,
			Lock:        &sync.Mutex{},
			PanelName:   req.PanelName,
			ReqId:       streamReqId,
			RequestType: "stream",
			Path:        streamId,
			IsStream:    true,
		}
		go func() {
			defer func() {
				if panicErr := recover(); panicErr != nil {
					log.Printf("PANIC streamFn %v\n", panicErr)
					log.Printf("%s\n", string(debug.Stack()))
				}
			}()
			streamFn(ctx, streamReq)
		}()
	}
	return nil
}

// SetBlobData sends blob data to the server.
// Note that SetBlobData will flush any pending actions to the server
func (req *PanelRequest) SetBlobData(path string, mimeType string, reader io.Reader) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call SetBlobData(), path=%s, PanelRequest is already done", path)
	}
	if !dashutil.IsMimeTypeValid(mimeType) {
		return fmt.Errorf("Invalid Mime-Type passed to SetBlobData mime-type=%s", mimeType)
	}
	first := true
	for {
		buffer := make([]byte, BLOB_READ_SIZE)
		n, err := io.ReadFull(reader, buffer)
		if err == io.EOF {
			break
		}
		if (err == nil || err == io.ErrUnexpectedEOF) && n > 0 {
			// write
			rrAction := &dashproto.RRAction{
				Ts:        dashutil.Ts(),
				Selector:  path,
				BlobBytes: buffer[0:n],
			}
			if first {
				rrAction.ActionType = "blob"
				rrAction.BlobMimeType = mimeType
				first = false
			} else {
				rrAction.ActionType = "blobext"
			}
			req.appendRR(rrAction)
			flushErr := req.Flush()
			if flushErr != nil {
				return flushErr
			}
		}
		if err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (req *PanelRequest) SetBlobDataFromFile(path string, mimeType string, fileName string) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer fd.Close()
	return req.SetBlobData(path, mimeType, fd)
}

// SetData is used to return data to the client.  Will replace the contents of path with data.
func (req *PanelRequest) SetData(path string, data interface{}) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call SetData(), path=%s, PanelRequest is already done", path)
	}
	jsonData, err := marshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SetData, path:%s, err:%v\n", path, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "setdata",
		Selector:   path,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
}

// SetHtml returns html to be rendered by the client.  Only valid for root handler requests (path = "/")
func (req *PanelRequest) SetHtml(html string) error {
	ts := dashutil.Ts()
	htmlAction := &dashproto.RRAction{
		Ts:         ts,
		ActionType: "html",
		Html:       html,
	}
	req.appendRR(htmlAction)
	return nil
}

// Convience wrapper over SetHtml that returns the contents of a file.
func (req *PanelRequest) SetHtmlFromFile(fileName string) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer fd.Close()
	htmlBytes, err := ioutil.ReadAll(fd)
	if err != nil {
		return err
	}
	return req.SetHtml(string(htmlBytes))
}

func (req *PanelRequest) isRootReq() bool {
	return req.RequestType == "handler" && req.PanelName != "" && req.Path == "/"
}

// Call from a handler to force the client to invalidate and re-pull data that matches path.
// Path is a regular expression.
func (req *PanelRequest) InvalidateData(path string) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call InvalidateData(), path=%s, PanelRequest is already done", path)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "invalidate",
		Selector:   path,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *PanelRequest) sendEvent(selector string, eventType string, data interface{}) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call SendEvent(), selector=%s, event=%s, PanelRequest is already done", selector, eventType)
	}
	jsonData, err := marshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SendEvent, selector:%s, event:%s, err:%v\n", selector, eventType, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "event",
		Selector:   selector,
		EventType:  eventType,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *PanelRequest) Flush() error {
	if req.IsDone {
		return fmt.Errorf("Cannot Flush(), PanelRequest is already done")
	}
	err := globalClient.sendRequestResponse(req, false)
	if err != nil && req.IsStream {
		logV("Dashborg Flush() stream error %v\n", err)
		globalClient.handleStreamClose(req)
	}
	return err
}

// Done() ends a request and sends the results back to the client.  It is automatically called after
// a handler/data-handler is run.  Only needs to be called explicitly if you'd like to return
// your result earlier.
func (req *PanelRequest) Done() error {
	if req.IsDone {
		return nil
	}
	if !req.AuthImpl && req.isRootReq() && req.Err == nil {
		AuthNone{}.checkAuth(req)
	}
	err := globalClient.sendRequestResponse(req, true)
	if err != nil {
		logV("Dashborg ERROR sending handler response: %v\n", err)
	}
	return err
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
	if path == "/" {
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
	jsonData, err := marshalJson(data)
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
	resp, err := globalClient.DBService.BackendPush(globalClient.ctxWithMd(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return errors.New("Error calling BackendPush()")
	}
	return nil
}

func ReflectZone() (*ZoneReflection, error) {
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
	var rtn ZoneReflection
	err = json.Unmarshal([]byte(resp.JsonData), &rtn)
	if err != nil {
		return nil, err
	}
	return &rtn, nil
}
