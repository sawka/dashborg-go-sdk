package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

// must be divisible by 3 (for base64 encoding)
const blobReadSize = 3 * 340 * 1024

// Request encapsulates all the data about a Dashborg request.  Normally the only
// fields that a handler needs to access are "Data" and "appState" in order to read
// the parameters and UI state associated with this request.  The other fields are
// exported, but subject to change and should not be used except in advanced use cases.

type RequestInfo struct {
	StartTime   time.Time
	ReqId       string // unique request id
	RequestType string // "data", "handler", or "stream"
	Path        string // handler or data path
	AppName     string // app name
	FeClientId  string // unique id for client
}

type RawRequestData struct {
	DataJson     string
	AppStateJson string
	AuthDataJson string
}

type Request interface {
	Context() context.Context
	AuthData() *AuthAtom
	RequestInfo() RequestInfo
	RawData() RawRequestData
}

type AppRequest struct {
	lock      *sync.Mutex     // synchronizes RRActions
	ctx       context.Context // gRPC context / streaming context
	info      RequestInfo
	rawData   RawRequestData
	appClient *appClient
	api       InternalApi
	appState  interface{}           // json-unmarshaled app state for this request
	authData  *AuthAtom             // authentication tokens associated with this request
	err       error                 // set if an error occured (when set, RRActions are not sent)
	rrActions []*dashproto.RRAction // output, these are the actions that will be returned
	isDone    bool                  // set after Done() is called and response has been sent to server
	infoMsgs  []string              // debugging information
}

func (req *AppRequest) canSetData(path string) bool {
	if path == RtnSetDataPath && req.info.RequestType == requestTypeData {
		return true
	}
	return req.info.RequestType == requestTypeHandler || req.info.RequestType == requestTypeStream || req.info.RequestType == requestTypeInit
}

func (req *AppRequest) canSetHtml() bool {
	return req.info.RequestType == requestTypeHandler || req.info.RequestType == requestTypeHtml
}

func (req *AppRequest) RequestInfo() RequestInfo {
	return req.info
}

func (req *AppRequest) Context() context.Context {
	return req.ctx
}

func (req *AppRequest) AuthData() *AuthAtom {
	return req.authData
}

func (req *AppRequest) UrlParams() url.Values {
	type UrlParamsState struct {
		UrlParams map[string]string `json:"urlparams"`
	}
	values := url.Values(make(map[string][]string))
	var state UrlParamsState
	err := req.BindAppState(&state)
	if err != nil {
		return values
	}
	for key, val := range state.UrlParams {
		values.Add(key, val)
	}
	return values
}

func (req *AppRequest) BindData(obj interface{}) error {
	if req.rawData.DataJson == "" {
		return nil
	}
	err := json.Unmarshal([]byte(req.rawData.DataJson), obj)
	return err
}

func (req *AppRequest) BindAppState(obj interface{}) error {
	if req.rawData.AppStateJson == "" {
		return nil
	}
	err := json.Unmarshal([]byte(req.rawData.AppStateJson), obj)
	return err
}

func (req *AppRequest) AppState() interface{} {
	return req.appState
}

func (req *AppRequest) appendRR(rrAction *dashproto.RRAction) {
	req.lock.Lock()
	defer req.lock.Unlock()
	req.rrActions = append(req.rrActions, rrAction)
}

func (req *AppRequest) clearActions() []*dashproto.RRAction {
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
func (req *AppRequest) StartStream(streamOpts StreamOpts, streamFn func(req *AppRequest)) error {
	if req.isDone {
		return fmt.Errorf("Cannot call StartStream(), Request is already done")
	}
	if req.isStream() {
		return fmt.Errorf("Cannot call StartStream(), Request is already streaming")
	}
	if !dashutil.IsUUIDValid(req.info.FeClientId) {
		return fmt.Errorf("No FeClientId, client does not support streaming")
	}
	streamReq, streamReqId, err := req.appClient.StartStream(req.info.AppName, streamOpts, req.info.FeClientId)
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
// Note that SetBlob will flush any pending actions to the server
func (req *AppRequest) SetBlob(path string, mimeType string, reader io.Reader) error {
	if req.isDone {
		return fmt.Errorf("Cannot call SetBlob(), path=%s, Request is already done", path)
	}
	if !req.canSetData(path) {
		return fmt.Errorf("Cannot call SetData(), reqinfo=%s data-path=%s reqtype=%s, for non-handler/stream request", req.reqInfoStr(), path, req.info.RequestType)
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

func (req *AppRequest) SetBlobFromFile(path string, mimeType string, fileName string) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer fd.Close()
	return req.SetBlob(path, mimeType, fd)
}

func (req *AppRequest) reqInfoStr() string {
	return fmt.Sprintf("%s://%s%s", req.info.RequestType, req.info.AppName, req.info.Path)
}

// SetData is used to return data to the client.  Will replace the contents of path with data.
func (req *AppRequest) DataOp(op string, path string, data interface{}) error {
	if req.isDone {
		return fmt.Errorf("Cannot call SetData(), reqinfo=%s data-path=%s, Request is already done", req.reqInfoStr())
	}
	if !req.canSetData(path) {
		return fmt.Errorf("Cannot call SetData(), reqinfo=%s data-path=%s reqtype=%s, for non-handler/stream request", req.reqInfoStr(), path, req.info.RequestType)
	}
	jsonData, err := dashutil.MarshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SetData, path:%s, err:%v\n", path, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "setdata",
		JsonData:   jsonData,
	}
	if op == "" || op == "set" {
		rrAction.Selector = path
	} else {
		rrAction.Selector = op + ":" + path
	}
	req.appendRR(rrAction)
	return nil
}

// SetData is used to return data to the client.  Will replace the contents of path with data.
func (req *AppRequest) SetData(path string, data interface{}) error {
	return req.DataOp("set", path, data)
}

// SetHtml returns html to be rendered by the client.  Only valid for root handler requests (path = "/")
func (req *AppRequest) setHtml(html string) error {
	if req.isDone {
		return fmt.Errorf("Cannot call SetHtml(), Request is already done")
	}
	if !req.canSetHtml() {
		return fmt.Errorf("Cannot call SetHtml() for request-type=%s", req.info.RequestType)
	}
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
func (req *AppRequest) setHtmlFromFile(fileName string) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer fd.Close()
	htmlBytes, err := ioutil.ReadAll(fd)
	if err != nil {
		return err
	}
	return req.setHtml(string(htmlBytes))
}

// Call from a handler to force the client to invalidate and re-pull data that matches path.
// Path is a regular expression. (e.g. use InvalidateData(".*") to invalidate all data).
func (req *AppRequest) InvalidateData(pathRegexp string) error {
	if req.isDone {
		return fmt.Errorf("Cannot call InvalidateData(), path=%s, Request is already done", pathRegexp)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "invalidate",
		Selector:   pathRegexp,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *AppRequest) Flush() error {
	if req.isDone {
		return fmt.Errorf("Cannot Flush(), Request is already done")
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
// a handler is run.  Only needs to be called explicitly if you'd like to return
// your result earlier, or for bare stream requests.
func (req *AppRequest) Done() error {
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

func (req *AppRequest) setAuthData(aa *AuthAtom) {
	if aa == nil {
		return
	}
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

func (req *AppRequest) isStream() bool {
	return req.info.RequestType == requestTypeStream
}

func (req *AppRequest) RawData() RawRequestData {
	return req.rawData
}

func (req *AppRequest) IsDone() bool {
	return req.isDone
}
