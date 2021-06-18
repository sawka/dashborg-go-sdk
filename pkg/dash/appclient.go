package dash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"google.golang.org/grpc/metadata"
)

const (
	ErrEOF         = "EOF"
	ErrUnknown     = "UNKNOWN"
	ErrBadConnId   = "BADCONNID"
	ErrAccAccess   = "ACCACCESS"
	ErrNoHandler   = "NOHANDLER"
	ErrUnavailable = "UNAVAILABLE"
)

const ClientVersion = "go-0.5.2"

const returnChSize = 20
const smallDrainSleep = 5 * time.Millisecond

type handlerKey struct {
	PanelName   string
	HandlerType string // data, handler
	Path        string
}

type handlerFuncType = func(*PanelRequest) (interface{}, error)

type handlerVal struct {
	ProtoHKey *dashproto.HandlerKey
	HandlerFn handlerFuncType
}

type streamControl struct {
	PanelName      string
	StreamOpts     StreamOpts
	ReqId          string
	Ctx            context.Context
	CancelFn       context.CancelFunc
	HasZeroClients bool
}

type streamKey struct {
	PanelName string
	StreamId  string
}

type AppClient interface {
	DispatchRequest(ctx context.Context, reqMsg *dashproto.RequestMessage)
	StartBareStream(panelName string, streamOpts StreamOpts) (*PanelRequest, error)
}

type appClient struct {
	Lock         *sync.Mutex
	StartTs      time.Time
	App          AppRuntime
	Config       *Config
	DBService    dashproto.DashborgServiceClient
	ConnId       *atomic.Value
	StreamMap    map[streamKey]streamControl // map streamKey -> streamControl
	StreamKeyMap map[string]streamKey        // map reqid -> streamKey
}

func MakeAppClient(app AppRuntime, service dashproto.DashborgServiceClient, config *Config, connId *atomic.Value) AppClient {
	rtn := &appClient{
		Lock:         &sync.Mutex{},
		StartTs:      time.Now(),
		App:          app,
		DBService:    service,
		Config:       config,
		ConnId:       connId,
		StreamMap:    make(map[streamKey]streamControl),
		StreamKeyMap: make(map[string]streamKey),
	}
	return rtn
}

// returns numStreamClients, err
func (pc *appClient) sendRequestResponse(req *PanelRequest, done bool) (int, error) {
	if req.isStream() && pc.stream_hasZeroClients(req.ReqId) {
		req.clearActions()
		return 0, nil
	}

	m := &dashproto.SendResponseMessage{
		Ts:           dashutil.Ts(),
		ReqId:        req.ReqId,
		RequestType:  req.RequestType,
		PanelName:    req.PanelName,
		FeClientId:   req.feClientId,
		ResponseDone: done,
	}
	if req.err != nil {
		m.Err = req.err.Error()
	}

	m.Actions = req.clearActions()

	if pc.ConnId.Load().(string) == "" {
		return 0, fmt.Errorf("No Active ConnId")
	}
	resp, err := pc.DBService.SendResponse(pc.ctxWithMd(), m)
	if err != nil {
		return 0, err
	}
	if resp.Err != "" {
		return 0, errors.New(resp.Err)
	}
	return int(resp.NumStreamClients), nil
}

func (pc *appClient) ctxWithMd() context.Context {
	ctx := context.Background()
	connId := pc.ConnId.Load().(string)
	ctx = metadata.AppendToOutgoingContext(ctx, "dashborg-connid", connId)
	return ctx
}

func (pc *appClient) sendWrongAppResponse(reqMsg *dashproto.RequestMessage) {
	m := &dashproto.SendResponseMessage{
		Ts:           dashutil.Ts(),
		ReqId:        reqMsg.ReqId,
		RequestType:  reqMsg.RequestType,
		PanelName:    reqMsg.PanelName,
		FeClientId:   reqMsg.FeClientId,
		ResponseDone: true,
		Err:          "Wrong AppName",
	}
	_, err := pc.DBService.SendResponse(pc.ctxWithMd(), m)
	if err != nil {
		pc.logV("Error sending No App Response: %v\n", err)
	}
}

func (pc *appClient) DispatchRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) {
	if reqMsg.PanelName != pc.App.GetAppName() {
		go pc.sendWrongAppResponse(reqMsg)
		return
	}

	if reqMsg.Err != "" {
		pc.logV("Dashborg gRPC got error request: err=%s\n", reqMsg.Err)
		return
	}
	pc.logV("Dashborg gRPC got request: panel=%s, type=%s, path=%s\n", reqMsg.PanelName, reqMsg.RequestType, reqMsg.Path)

	preq := &PanelRequest{
		StartTime:     time.Now(),
		ctx:           ctx,
		lock:          &sync.Mutex{},
		PanelName:     reqMsg.PanelName,
		ReqId:         reqMsg.ReqId,
		RequestType:   reqMsg.RequestType,
		feClientId:    reqMsg.FeClientId,
		Path:          reqMsg.Path,
		isBackendCall: reqMsg.IsBackendCall,
		appClient:     pc,
	}
	hkey := handlerKey{
		PanelName: reqMsg.PanelName,
		Path:      reqMsg.Path,
	}
	switch reqMsg.RequestType {
	case "data":
		hkey.HandlerType = "data"
	case "handler":
		hkey.HandlerType = "handler"
	case "html":
		hkey.HandlerType = "html"
	case "auth":
		hkey.HandlerType = "auth"
	case "streamclose":
		// *** pc.stream_serverStop(preq.ReqId)
		return // no response for streamclose
	default:
		preq.err = fmt.Errorf("Invalid RequestMessage.RequestType [%s]", reqMsg.RequestType)
		preq.Done()
		return
	}

	var data interface{}
	if reqMsg.JsonData != "" {
		err := json.Unmarshal([]byte(reqMsg.JsonData), &data)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal JsonData: %v", err)
			preq.Done()
			return
		}
	}
	preq.Data = data
	preq.DataJson = reqMsg.JsonData

	var pstate interface{}
	if reqMsg.PanelStateData != "" {
		err := json.Unmarshal([]byte(reqMsg.PanelStateData), &pstate)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal PanelStateData: %v", err)
			preq.Done()
			return
		}
	}
	preq.PanelState = pstate
	preq.PanelStateJson = reqMsg.PanelStateData

	var authData AuthAtom
	if reqMsg.AuthData != "" {
		err := json.Unmarshal([]byte(reqMsg.AuthData), &authData)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal AuthData: %v", err)
			preq.Done()
			return
		}
		preq.AuthData = &authData
	}

	isAllowedBackendCall := preq.isBackendCall && preq.RequestType == "data" && pc.Config.AllowBackendCalls

	// check-auth
	if !isAllowedBackendCall && preq.RequestType != "auth" {
		if !preq.isAuthenticated() {
			preq.err = fmt.Errorf("Request is not authenticated")
			preq.Done()
			return
		}
	}

	defer func() {
		if panicErr := recover(); panicErr != nil {
			log.Printf("Dashborg PANIC in Handler %v | %v\n", hkey, panicErr)
			preq.err = fmt.Errorf("PANIC in handler %v", panicErr)
			debug.PrintStack()
		}
		preq.Done()
	}()
	var dataResult interface{}
	dataResult, preq.err = pc.App.RunHandler(preq)
	if hkey.HandlerType == "data" {
		jsonData, err := dashutil.MarshalJson(dataResult)
		if err != nil {
			preq.err = err
			return
		}
		rrAction := &dashproto.RRAction{
			Ts:         dashutil.Ts(),
			ActionType: "setdata",
			JsonData:   jsonData,
		}
		preq.appendRR(rrAction)
	}
}

func nonBlockingSend(returnCh chan *dashproto.RRAction, rrAction *dashproto.RRAction) error {
	select {
	case returnCh <- rrAction:
		return nil
	default:
		return fmt.Errorf("Send would block")
	}
}

func nonBlockingRecv(returnCh chan *dashproto.RRAction) *dashproto.RRAction {
	select {
	case rtn := <-returnCh:
		return rtn
	default:
		return nil
	}
}

// returns a context (reqid, ctx, nil) if SDK should start a new stream.
// returns reqid, nil, nil if successfully hooked this client up to the existing streaming function.
// returns "", nil, err if there was an error.
func (pc *appClient) startStream(panelName string, feClientId string, streamOpts StreamOpts) (string, context.Context, error) {
	reqId := pc.stream_getReqId(streamKey{PanelName: panelName, StreamId: streamOpts.StreamId})
	m := &dashproto.StartStreamMessage{
		Ts:            dashutil.Ts(),
		PanelName:     panelName,
		FeClientId:    feClientId,
		ExistingReqId: reqId,
	}
	resp, err := pc.DBService.StartStream(pc.ctxWithMd(), m)
	if err != nil {
		pc.logV("Dashborg procclient startStream error: %v\n", err)
		return "", nil, fmt.Errorf("Dashborg procclient startStream error: %w", err)
	}
	if !resp.Success {
		return "", nil, fmt.Errorf("Dashborg procclient startStream error: %s", resp.Err)
	}
	if reqId != "" && reqId != resp.ReqId {
		return "", nil, fmt.Errorf("Dashborg procclient startStream returned reqid:%s does not match existing reqid:%s", resp.ReqId, reqId)
	}
	reqId = resp.ReqId
	sc := streamControl{
		PanelName:      panelName,
		StreamOpts:     streamOpts,
		ReqId:          reqId,
		HasZeroClients: false,
	}
	var shouldStart bool
	sc, shouldStart = pc.stream_clientStart(sc, feClientId)
	if !shouldStart {
		return sc.ReqId, nil, nil
	}
	return sc.ReqId, sc.Ctx, nil
}

func (pc *appClient) logV(fmtStr string, args ...interface{}) {
	if pc.Config.Verbose {
		log.Printf(fmtStr, args...)
	}
}
