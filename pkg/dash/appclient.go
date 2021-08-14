package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const returnChSize = 20
const smallDrainSleep = 5 * time.Millisecond

type handlerKey struct {
	AppName     string
	HandlerType string // data, handler
	Path        string
}

type handlerFuncType = func(*Request) (interface{}, error)

type handlerVal struct {
	ProtoHKey *dashproto.HandlerKey
	HandlerFn handlerFuncType
}

type streamControl struct {
	AppName        string
	StreamOpts     StreamOpts
	ReqId          string
	Ctx            context.Context
	CancelFn       context.CancelFunc
	HasZeroClients bool
}

type streamKey struct {
	AppName  string
	StreamId string
}

type AppClient interface {
	DispatchRequest(ctx context.Context, reqMsg *dashproto.RequestMessage)
	SendRequestResponse(req *Request, done bool) (int, error)
	StartStream(appName string, streamOpts StreamOpts, feClientId string) (*Request, string, error)
}

type DBServiceAdapter interface {
	SendResponseProtoRpc(m *dashproto.SendResponseMessage) (int, error)
	StartStreamProtoRpc(m *dashproto.StartStreamMessage) (string, error)
}

type AppClientConfig struct {
	Verbose bool
}

type appClient struct {
	Lock             *sync.Mutex
	StartTs          time.Time
	App              AppRuntime
	Config           AppClientConfig
	DBServiceAdapter DBServiceAdapter
	ConnId           *atomic.Value
	StreamMap        map[streamKey]streamControl // map streamKey -> streamControl
	StreamKeyMap     map[string]streamKey        // map reqid -> streamKey
	Container        Container
}

func MakeAppClient(container Container, app AppRuntime, service DBServiceAdapter, config AppClientConfig, connId *atomic.Value) AppClient {
	rtn := &appClient{
		Lock:             &sync.Mutex{},
		StartTs:          time.Now(),
		App:              app,
		DBServiceAdapter: service,
		Config:           config,
		ConnId:           connId,
		StreamMap:        make(map[streamKey]streamControl),
		StreamKeyMap:     make(map[string]streamKey),
		Container:        container,
	}
	return rtn
}

// returns numStreamClients, err
func (pc *appClient) SendRequestResponse(req *Request, done bool) (int, error) {
	if req.isStream() && pc.stream_hasZeroClients(req.info.ReqId) {
		req.clearActions()
		return 0, nil
	}
	m := &dashproto.SendResponseMessage{
		Ts:           dashutil.Ts(),
		ReqId:        req.info.ReqId,
		RequestType:  req.info.RequestType,
		PanelName:    req.info.AppName,
		FeClientId:   req.info.FeClientId,
		ResponseDone: done,
	}
	if req.err != nil {
		m.Err = req.err.Error()
		req.clearActions()
	} else {
		m.Actions = req.clearActions()
	}
	return pc.DBServiceAdapter.SendResponseProtoRpc(m)
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
	pc.DBServiceAdapter.SendResponseProtoRpc(m)
}

func (pc *appClient) DispatchRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) {
	if reqMsg.PanelName != pc.App.GetAppConfig().AppName {
		go pc.sendWrongAppResponse(reqMsg)
		return
	}

	if reqMsg.Status != nil {
		dashErr := dasherr.FromRtnStatus("RequestStream", reqMsg.Status)
		if dashErr != nil {
			pc.logV("DashborgAppClient %v\n", dashErr)
		}
		return
	}

	preq := &Request{
		info: RequestInfo{
			StartTime:   time.Now(),
			ReqId:       reqMsg.ReqId,
			RequestType: reqMsg.RequestType,
			AppName:     reqMsg.PanelName,
			Path:        reqMsg.Path,
			FeClientId:  reqMsg.FeClientId,
		},
		ctx:       ctx,
		lock:      &sync.Mutex{},
		appClient: pc,
		container: pc.Container,
	}
	hkey := handlerKey{
		AppName: reqMsg.PanelName,
		Path:    reqMsg.Path,
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
	case "init":
		hkey.HandlerType = "init"
	case "streamclose":
		pc.stream_serverStop(preq.info.ReqId)
		return // no response for streamclose
	default:
		preq.err = fmt.Errorf("Invalid RequestMessage.RequestType [%s]", reqMsg.RequestType)
		preq.Done()
		return
	}

	preq.dataJson = reqMsg.JsonData

	var pstate interface{}
	if reqMsg.PanelStateData != "" {
		err := json.Unmarshal([]byte(reqMsg.PanelStateData), &pstate)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal PanelStateData: %v", err)
			preq.Done()
			return
		}
	}
	preq.appState = pstate
	preq.appStateJson = reqMsg.PanelStateData

	var authData AuthAtom
	if reqMsg.AuthData != "" {
		err := json.Unmarshal([]byte(reqMsg.AuthData), &authData)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal authData: %v", err)
			preq.Done()
			return
		}
		preq.authData = &authData
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
	if preq.err != nil {
		return
	}
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

func (pc *appClient) logV(fmtStr string, args ...interface{}) {
	if pc.Config.Verbose {
		log.Printf(fmtStr, args...)
	}
}
