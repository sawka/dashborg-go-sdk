package dash

import (
	"context"
	"encoding/json"
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
const blobReadSize = 3 * 340 * 1024

// PanelRequest encapsulates all the data about a Dashborg request.  Normally the only
// fields that a handler needs to access are "Data" and "appState" in order to read
// the parameters and UI state associated with this request.  The other fields are
// exported, but subject to change and should not be used except in advanced use cases.

type RequestInfo struct {
	StartTime   time.Time
	ReqId       string // unique request id
	RequestType string // "data", "handler", or "stream"
	Path        string // handler or data path
	AppName     string // app name
}

type PanelRequest struct {
	info RequestInfo

	rawDataJson  string      // Raw JSON for Data (used for manual unmarshalling into custom struct)
	appState     interface{} // json-unmarshaled panel state for this request
	appStateJson string      // Raw JSON for appState (used for manual unmarshalling into custom struct)
	authData     *AuthAtom   // authentication tokens associated with this request

	infoMsgs   []string              // debugging information
	err        error                 // set if an error occured (when set, RRActions are not sent)
	rrActions  []*dashproto.RRAction // output, these are the actions that will be returned
	lock       *sync.Mutex           // synchronizes RRActions
	feClientId string                // unique id for client
	ctx        context.Context       // gRPC context / streaming context
	isDone     bool                  // set after Done() is called and response has been sent to server

	appClient *appClient
	container Container
}

type PanelRequestEx struct {
	Req *PanelRequest
}

func (req *PanelRequest) RequestInfo() RequestInfo {
	return req.info
}

func (req *PanelRequest) Context() context.Context {
	return req.ctx
}

func (req *PanelRequest) Container() Container {
	return req.container
}

func (req *PanelRequest) AuthData() *AuthAtom {
	return req.authData
}

func (req *PanelRequest) RawDataJson() string {
	return req.rawDataJson
}

func (req *PanelRequest) BindData(obj interface{}) error {
	if req.rawDataJson == "" {
		return nil
	}
	err := json.Unmarshal([]byte(req.rawDataJson), obj)
	return err
}

func (req *PanelRequest) BindAppState(obj interface{}) error {
	if req.appStateJson == "" {
		return nil
	}
	err := json.Unmarshal([]byte(req.appStateJson), obj)
	return err
}

func (req *PanelRequest) AppState() interface{} {
	return req.appState
}

func (req *PanelRequest) appendRR(rrAction *dashproto.RRAction) {
	req.lock.Lock()
	defer req.lock.Unlock()
	req.rrActions = append(req.rrActions, rrAction)
}

func (req *PanelRequest) clearActions() []*dashproto.RRAction {
	req.lock.Lock()
	defer req.lock.Unlock()
	rtn := req.rrActions
	req.rrActions = nil
	return rtn
}

// StartStream creates a new streaming request that can send data to the original request's client.
// streamId is used to control whether a new stream will be created or if the client will attach to
// an existing stream.  The streamFn gets passed a context that is used for cancelation.
// Note that StartStream will flush any pending actions to the server.
// If the stream already exists, the existing StreamOpts will not change (keeps the old NoServerCancel setting).
// streamFn may be nil (useful if you are intending to attach to an existing stream created with StartBareStream).
func (req *PanelRequest) StartStream(streamOpts StreamOpts, streamFn func(req *PanelRequest)) error {
	if req.isDone {
		return fmt.Errorf("Cannot call StartStream(), PanelRequest is already done")
	}
	if req.isStream() {
		return fmt.Errorf("Cannot call StartStream(), PanelRequest is already streaming")
	}
	if !dashutil.IsUUIDValid(req.feClientId) {
		return fmt.Errorf("No FeClientId, client does not support streaming")
	}
	streamReq, streamReqId, err := req.appClient.StartStream(req.info.AppName, streamOpts, req.feClientId)
	if err != nil {
		return err
	}
	data := map[string]interface{}{
		"reqid":       streamReqId,
		"controlpath": streamOpts.ControlPath,
	}
	jsonData, _ := dashutil.MarshalJson(data)
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "streamopen",
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	req.Flush() // TODO flush error
	if streamReq != nil {
		go func() {
			defer func() {
				if panicErr := recover(); panicErr != nil {
					log.Printf("PANIC streamFn %v\n", panicErr)
					log.Printf("%s\n", string(debug.Stack()))
				}
				streamReq.Done()
			}()
			if streamFn != nil {
				streamFn(streamReq)
			}
		}()
	}
	return nil
}

// SetBlobData sends blob data to the server.
// Note that SetBlobData will flush any pending actions to the server
func (req *PanelRequest) SetBlobData(path string, mimeType string, reader io.Reader) error {
	if req.isDone {
		return fmt.Errorf("Cannot call SetBlobData(), path=%s, PanelRequest is already done", path)
	}
	if !dashutil.IsMimeTypeValid(mimeType) {
		return fmt.Errorf("Invalid Mime-Type passed to SetBlobData mime-type=%s", mimeType)
	}
	first := true
	for {
		buffer := make([]byte, blobReadSize)
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
func (req *PanelRequest) DataOp(op string, path string, data interface{}) error {
	if req.isDone {
		return fmt.Errorf("Cannot call SetData(), path=%s, PanelRequest is already done", path)
	}
	jsonData, err := dashutil.MarshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SetData, path:%s, err:%v\n", path, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "setdata",
		OpType:     op,
		Selector:   path,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
}

// SetData is used to return data to the client.  Will replace the contents of path with data.
func (req *PanelRequest) SetData(path string, data interface{}) error {
	return req.DataOp("set", path, data)
}

// SetHtml returns html to be rendered by the client.  Only valid for root handler requests (path = "/")
func (req *PanelRequest) setHtml(html string) error {
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
func (req *PanelRequest) setHtmlFromFile(fileName string) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer fd.Close()
	htmlBytes, err := ioutil.ReadAll(fd)
	if err != nil {
		return err
	}
	return PanelRequestEx{req}.SetHtml(string(htmlBytes))
}

func (req *PanelRequest) isRootReq() bool {
	return req.info.RequestType == "handler" && req.info.AppName != "" && req.info.Path == "/"
}

// Call from a handler to force the client to invalidate and re-pull data that matches path.
// Path is a regular expression. (e.g. use InvalidateData(".*") to invalidate all data).
func (req *PanelRequest) InvalidateData(pathRegexp string) error {
	if req.isDone {
		return fmt.Errorf("Cannot call InvalidateData(), path=%s, PanelRequest is already done", pathRegexp)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "invalidate",
		Selector:   pathRegexp,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *PanelRequest) Flush() error {
	if req.isDone {
		return fmt.Errorf("Cannot Flush(), PanelRequest is already done")
	}
	numStreamClients, err := req.appClient.SendRequestResponse(req, false)
	if req.isStream() && err != nil {
		req.appClient.logV("Dashborg Flush() stream error %v\n", err)
		req.appClient.stream_serverStop(req.info.ReqId)
	} else if req.isStream() && numStreamClients == 0 {
		req.appClient.stream_handleZeroClients(req.info.ReqId)
	}
	return err
}

// Done() ends a request and sends the results back to the client.  It is automatically called after
// a handler/data-handler is run.  Only needs to be called explicitly if you'd like to return
// your result earlier.
func (req *PanelRequest) Done() error {
	if req.isDone {
		return nil
	}
	req.isDone = true
	_, err := req.appClient.SendRequestResponse(req, true)
	if err != nil {
		req.appClient.logV("Dashborg ERROR sending handler response: %v\n", err)
	}
	if req.isStream() {
		req.appClient.stream_clientStop(req.info.ReqId)
	}
	return err
}

func (req *PanelRequest) IsDone() bool {
	return req.isDone
}

func (req *PanelRequest) setAuthData(aa AuthAtom) {
	if aa.Ts == 0 {
		aa.Ts = dashutil.Ts() + int64(MaxAuthExp/time.Millisecond)
	}
	if aa.Type == "" {
		panic(fmt.Sprintf("Dashborg Invalid AuthAtom, no Type specified"))
	}
	jsonAa, _ := json.Marshal(aa)
	rr := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "panelauth",
		JsonData:   string(jsonAa),
	}
	req.appendRR(rr)
}

func (req *PanelRequest) isStream() bool {
	return req.info.RequestType == "stream"
}

func (req *PanelRequest) isAuthenticated() bool {
	return req.authData != nil
}

func (rex PanelRequestEx) SetAuthData(aa AuthAtom) {
	rex.Req.setAuthData(aa)
}

func (rex PanelRequestEx) SetHtml(html string) error {
	return rex.Req.setHtml(html)
}

func (rex PanelRequestEx) SetHtmlFromFile(fileName string) error {
	return rex.Req.setHtmlFromFile(fileName)
}

func (rex PanelRequestEx) AppendPanelAuthChallenge(ch interface{}) error {
	challengeJson, err := dashutil.MarshalJson(ch)
	if err != nil {
		return err
	}
	rex.Req.appendRR(&dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "panelauthchallenge",
		JsonData:   string(challengeJson),
	})
	return nil
}

func (rex PanelRequestEx) AppendInfoMessage(message string) {
	rex.Req.infoMsgs = append(rex.Req.infoMsgs, message)
}
