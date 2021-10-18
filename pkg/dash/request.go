package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const htmlPagePath = "$state.dashborg.htmlpage"
const pageNameKey = "apppage"

// Request encapsulates all the data about a Dashborg request.  Normally the only
// fields that a handler needs to access are "Data" and "appState" in order to read
// the parameters and UI state associated with this request.  The other fields are
// exported, but subject to change and should not be used except in advanced use cases.

type RequestInfo struct {
	StartTime     time.Time
	ReqId         string // unique request id
	RequestType   string // "data", "handler", or "stream"
	RequestMethod string // GET or POST
	Path          string // request path
	AppName       string // app name
	FeClientId    string // unique id for client
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
	BindData(obj interface{}) error
	BindAppState(obj interface{}) error
}

type dashborgState struct {
	UrlParams  map[string]interface{} `json:"urlparams"`
	PostParams map[string]interface{} `json:"postparams"`
	Dashborg   map[string]interface{} `json:"dashborg"`
}

type AppRequest struct {
	lock      *sync.Mutex     // synchronizes RRActions
	ctx       context.Context // gRPC context / streaming context
	info      RequestInfo
	rawData   RawRequestData
	client    *DashCloudClient
	appState  interface{}           // json-unmarshaled app state for this request
	authData  *AuthAtom             // authentication tokens associated with this request
	err       error                 // set if an error occured (when set, RRActions are not sent)
	rrActions []*dashproto.RRAction // output, these are the actions that will be returned
	isDone    bool                  // set after Done() is called and response has been sent to server
	infoMsgs  []string              // debugging information
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

// SetBlobData sends blob data to the server.
// Note that SetBlob will flush any pending actions to the server
func (req *AppRequest) SetBlob(path string, mimeType string, reader io.Reader) error {
	if req.isDone {
		return fmt.Errorf("Cannot call SetBlob(), path=%s, Request is already done", path)
	}
	actions, err := blobToRRA(mimeType, reader)
	if err != nil {
		return err
	}
	for _, rrAction := range actions {
		req.appendRR(rrAction)
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
	return fmt.Sprintf("%s://%s", req.info.RequestType, req.info.Path)
}

// SetData is used to return data to the client.  Will replace the contents of path with data.
func (req *AppRequest) AddDataOp(op string, path string, data interface{}) error {
	if req.isDone {
		return fmt.Errorf("Cannot call SetData(), reqinfo=%s data-path=%s, Request is already done", req.reqInfoStr())
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
	return req.AddDataOp("set", path, data)
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

func makeAppRequest(ctx context.Context, reqMsg *dashproto.RequestMessage, client *DashCloudClient) *AppRequest {
	preq := &AppRequest{
		info: RequestInfo{
			StartTime:     time.Now(),
			ReqId:         reqMsg.ReqId,
			RequestType:   reqMsg.RequestType,
			RequestMethod: reqMsg.RequestMethod,
			Path:          reqMsg.Path,
			FeClientId:    reqMsg.FeClientId,
		},
		rawData: RawRequestData{
			DataJson:     reqMsg.JsonData,
			AuthDataJson: reqMsg.AuthData,
			AppStateJson: reqMsg.AppStateData,
		},
		ctx:    ctx,
		lock:   &sync.Mutex{},
		client: client,
	}
	preq.info.AppName = dashutil.AppNameFromPath(reqMsg.Path)
	if !dashutil.IsRequestTypeValid(reqMsg.RequestType) {
		preq.err = fmt.Errorf("Invalid RequestMessage.RequestType [%s]", reqMsg.RequestType)
		return preq
	}
	if reqMsg.AuthData != "" {
		var authData AuthAtom
		err := json.Unmarshal([]byte(reqMsg.AuthData), &authData)
		if err != nil {
			preq.err = dasherr.JsonUnmarshalErr("AuthData", err)
			return preq
		}
		preq.authData = &authData
	}
	if reqMsg.AppStateData != "" {
		var pstate interface{}
		err := json.Unmarshal([]byte(reqMsg.AppStateData), &pstate)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal AppStateData: %v", err)
			return preq
		}
		preq.appState = pstate
	}
	return preq
}

func (req *AppRequest) getRRA() []*dashproto.RRAction {
	return req.rrActions
}

func (req *AppRequest) GetError() error {
	return req.err
}

func (req *AppRequest) SetError(err error) {
	req.err = err
}

func (req *AppRequest) SetHtmlPage(htmlPage string) error {
	_, _, err := dashutil.ParseHtmlPage(htmlPage)
	if err != nil {
		return err
	}
	req.SetData(htmlPagePath, htmlPage)
	return nil
}

func (req *AppRequest) GetPageName() string {
	var state dashborgState
	err := req.BindAppState(&state)
	if err != nil {
		return ""
	}
	strRtn, ok := state.Dashborg[pageNameKey].(string)
	if !ok {
		return ""
	}
	return strRtn
}

func (req *AppRequest) NavToPage(pageName string, params interface{}) error {
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "navto",
		Selector:   pageName,
	}
	if params != nil {
		jsonData, err := dashutil.MarshalJson(params)
		if err != nil {
			return fmt.Errorf("Error marshaling json for NavToPage, pageName:%s, err:%v\n", pageName, err)
		}
		rrAction.JsonData = jsonData
	}
	req.appendRR(rrAction)
	return nil
}
