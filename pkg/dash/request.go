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

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

// PanelRequest encapsulates all the data about a Dashborg request.  Normally the only
// fields that a handler needs to access are "Data" and "PanelState" in order to read
// the parameters and UI state associated with this request.  The other fields are
// exported, but subject to change and should not be used except in advanced use cases.
type PanelRequest struct {
	StartTime      time.Time
	ReqId          string      // unique request id
	RequestType    string      // "data", "handler", or "stream"
	Path           string      // handler or data path
	PanelName      string      // panel name
	Data           interface{} // json-unmarshaled data attached to this request
	DataJson       string      // Raw JSON for Data (used for manual unmarshalling into custom struct)
	PanelState     interface{} // json-unmarshaled panel state for this request
	PanelStateJson string      // Raw JSON for PanelState (used for manual unmarshalling into custom struct)
	AuthData       *AuthAtom   // authentication tokens associated with this request

	info          []string              // debugging information
	err           error                 // set if an error occured (when set, RRActions are not sent)
	authImpl      bool                  // if not set, will default NoAuth() on Done()
	rrActions     []*dashproto.RRAction // output, these are the actions that will be returned
	lock          *sync.Mutex           // synchronizes RRActions
	feClientId    string                // unique id for client
	ctx           context.Context       // gRPC context / streaming context
	isDone        bool                  // set after Done() is called and response has been sent to server
	isBackendCall bool                  // true if this request originated from a backend data call
}

type PanelRequestEx struct {
	Req *PanelRequest
}

func (req *PanelRequest) Context() context.Context {
	return req.ctx
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
func (req *PanelRequest) StartStream(streamOpts StreamOpts, streamFn func(ctx context.Context, req *PanelRequest)) error {
	if streamOpts.StreamId == "" {
		streamOpts.StreamId = uuid.New().String()
	}
	if !dashutil.IsTagValid(streamOpts.StreamId) {
		return fmt.Errorf("Invalid StreamId")
	}
	if !dashutil.IsUUIDValid(req.feClientId) {
		return fmt.Errorf("No FeClientId, client does not support streaming")
	}
	if req.isDone {
		return fmt.Errorf("Cannot call StartStream(), PanelRequest is already done")
	}
	if req.isStream() {
		return fmt.Errorf("Cannot call StartStream(), PanelRequest is already streaming")
	}
	streamReqId, ctx, err := globalClient.startStream(req.PanelName, req.feClientId, streamOpts)
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
	if ctx != nil {
		streamReq := &PanelRequest{
			StartTime:   time.Now(),
			ctx:         ctx,
			lock:        &sync.Mutex{},
			PanelName:   req.PanelName,
			ReqId:       streamReqId,
			RequestType: "stream",
			Path:        streamOpts.StreamId,
		}
		go func() {
			defer func() {
				if panicErr := recover(); panicErr != nil {
					log.Printf("PANIC streamFn %v\n", panicErr)
					log.Printf("%s\n", string(debug.Stack()))
				}
				streamReq.Done()
			}()
			if streamFn != nil {
				streamFn(ctx, streamReq)
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
		buffer := make([]byte, _BLOB_READ_SIZE)
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
		Selector:   path,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
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
	return req.RequestType == "handler" && req.PanelName != "" && req.Path == "/"
}

// Call from a handler to force the client to invalidate and re-pull data that matches path.
// Path is a regular expression.
func (req *PanelRequest) InvalidateData(path string) error {
	if req.isDone {
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
	if req.isDone {
		return fmt.Errorf("Cannot call SendEvent(), selector=%s, event=%s, PanelRequest is already done", selector, eventType)
	}
	jsonData, err := dashutil.MarshalJson(data)
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
	if req.isDone {
		return fmt.Errorf("Cannot Flush(), PanelRequest is already done")
	}
	numStreamClients, err := globalClient.sendRequestResponse(req, false)
	if req.isStream() && err != nil {
		logV("Dashborg Flush() stream error %v\n", err)
		globalClient.stream_serverStop(req.ReqId)
	} else if req.isStream() && numStreamClients == 0 {
		globalClient.stream_handleZeroClients(req.ReqId)
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
	if !req.authImpl && req.isRootReq() && req.err == nil {
		AuthNone{}.checkAuth(req)
	}
	_, err := globalClient.sendRequestResponse(req, true)
	if err != nil {
		logV("Dashborg ERROR sending handler response: %v\n", err)
	}
	if req.isStream() {
		globalClient.stream_clientStop(req.ReqId)
	}
	return err
}

func (req *PanelRequest) setAuthData(aa AuthAtom) {
	if aa.Scope == "" {
		aa.Scope = fmt.Sprintf("panel:%s:%s", globalClient.Config.ZoneName, req.PanelName)
	}
	if aa.Ts == 0 {
		aa.Ts = dashutil.Ts() + int64(_MAX_AUTH_EXP/time.Millisecond)
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

func (req *PanelRequest) appendPanelAuthChallenge(ch authChallenge) {
	challengeJson, _ := json.Marshal(ch)
	req.appendRR(&dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "panelauthchallenge",
		JsonData:   string(challengeJson),
	})
	return
}

// If AllowedAuth impelementations return an error they will be logged into
// req.Info.  They will not stop the execution of the function since
// other auth methods might succeed.
func (req *PanelRequest) checkAuth(allowedAuths ...AllowedAuth) bool {
	req.authImpl = true
	if (PanelRequestEx{req}).IsAuthenticated() {
		return true
	}
	for _, aa := range allowedAuths {
		ok, err := aa.checkAuth(req)
		if err != nil {
			req.info = append(req.info, err.Error())
		}
		if ok {
			return true
		}
	}
	for _, aa := range allowedAuths {
		if chAuth, ok := aa.(challengeAuth); ok {
			ch := chAuth.returnChallenge(req)
			if ch != nil {
				req.appendPanelAuthChallenge(*ch)
			}
		}
	}
	return false
}

func (req *PanelRequest) isStream() bool {
	return req.RequestType == "stream"
}

func (rex PanelRequestEx) SetHtml(html string) error {
	return rex.Req.setHtml(html)
}

func (rex PanelRequestEx) CheckAuth(allowedAuths ...AllowedAuth) bool {
	return rex.Req.checkAuth(allowedAuths...)
}

func (rex PanelRequestEx) IsDone() bool {
	return rex.Req.isDone
}

func (rex PanelRequestEx) IsBackendCall() bool {
	return rex.Req.isBackendCall
}

func (rex PanelRequestEx) IsAuthenticated() bool {
	if globalClient.Config.LocalServer {
		return true
	}
	return rex.Req.AuthData != nil
}

func (rex PanelRequestEx) SetAuthData(aa AuthAtom) {
	rex.Req.setAuthData(aa)
}
