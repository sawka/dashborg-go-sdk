package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type handlerFuncType = func(*Request) (interface{}, error)

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

// internal interface (for communication with Dashborg Cloud Service)
// API is not stable
type AppClient interface {
	DispatchRequest(ctx context.Context, reqMsg *dashproto.RequestMessage)
	SendRequestResponse(req *Request, done bool) (int, error)
	StartStream(appName string, streamOpts StreamOpts, feClientId string) (*Request, string, error)
}

// internal callbacks into DashCloud package.  Not for use by end-user, API subject to change.
type InternalApi interface {
	// StartBareStream(appName string, streamOpts StreamOpts) (*Request, error)
	// BackendPush(appName string, path string, data interface{}) error
	SetBlobData(acfg AppConfig, blob BlobData, r io.Reader) error
	RemoveBlob(acfg AppConfig, blob BlobData) error
	ListBlobs(appName string, appVersion string) ([]BlobData, error)
	SendResponseProtoRpc(m *dashproto.SendResponseMessage) (int, error)
	StartStreamProtoRpc(m *dashproto.StartStreamMessage) (string, error)
}

type AppClientConfig struct {
	Verbose bool
}

type appClient struct {
	Lock         *sync.Mutex
	StartTs      time.Time
	App          AppRuntime
	Config       AppClientConfig
	ConnId       *atomic.Value
	StreamMap    map[streamKey]streamControl // map streamKey -> streamControl
	StreamKeyMap map[string]streamKey        // map reqid -> streamKey
	Api          InternalApi
}

func MakeAppClient(api InternalApi, app AppRuntime, config AppClientConfig, connId *atomic.Value) AppClient {
	rtn := &appClient{
		Lock:         &sync.Mutex{},
		StartTs:      time.Now(),
		App:          app,
		Config:       config,
		ConnId:       connId,
		StreamMap:    make(map[streamKey]streamControl),
		StreamKeyMap: make(map[string]streamKey),
		Api:          api,
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
		AppId:        &dashproto.AppId{AppName: req.info.AppName},
		FeClientId:   req.info.FeClientId,
		ResponseDone: done,
	}
	if req.err != nil {
		m.Err = req.err.Error()
		req.clearActions()
	} else {
		m.Actions = req.clearActions()
	}
	return pc.Api.SendResponseProtoRpc(m)
}

func (pc *appClient) sendErrResponse(reqMsg *dashproto.RequestMessage, errMsg string) {
	m := &dashproto.SendResponseMessage{
		Ts:           dashutil.Ts(),
		ReqId:        reqMsg.ReqId,
		RequestType:  reqMsg.RequestType,
		AppId:        reqMsg.AppId,
		FeClientId:   reqMsg.FeClientId,
		ResponseDone: true,
		Err:          errMsg,
	}
	pc.Api.SendResponseProtoRpc(m)
}

func (pc *appClient) DispatchRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) {
	if reqMsg.AppId.AppName != pc.App.AppName() {
		go pc.sendErrResponse(reqMsg, "Wrong AppName")
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
			AppName:     reqMsg.AppId.AppName,
			Path:        reqMsg.Path,
			FeClientId:  reqMsg.FeClientId,
		},
		ctx:       ctx,
		lock:      &sync.Mutex{},
		appClient: pc,
		api:       pc.Api,
	}
	if !dashutil.IsRequestTypeValid(reqMsg.RequestType) {
		preq.err = fmt.Errorf("Invalid RequestMessage.RequestType [%s]", reqMsg.RequestType)
		preq.Done()
		return
	}
	if reqMsg.RequestType == "streamclose" {
		pc.stream_serverStop(preq.info.ReqId)
		return // no response for streamclose
	}
	preq.rawData.DataJson = reqMsg.JsonData
	if reqMsg.AppStateData != "" {
		var pstate interface{}
		err := json.Unmarshal([]byte(reqMsg.AppStateData), &pstate)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal AppStateData: %v", err)
			preq.Done()
			return
		}
		preq.appState = pstate
		preq.rawData.AppStateJson = reqMsg.AppStateData
	}

	if reqMsg.AuthData != "" {
		var authData AuthAtom
		err := json.Unmarshal([]byte(reqMsg.AuthData), &authData)
		if err != nil {
			preq.err = fmt.Errorf("Cannot unmarshal authData: %v", err)
			preq.Done()
			return
		}
		preq.authData = &authData
		preq.rawData.AuthDataJson = reqMsg.AuthData
	}

	defer func() {
		if panicErr := recover(); panicErr != nil {
			log.Printf("Dashborg PANIC in Handler %s:%s | %v\n", reqMsg.RequestType, reqMsg.Path, panicErr)
			preq.err = fmt.Errorf("PANIC in handler %v", panicErr)
			debug.PrintStack()
		}
		preq.Done()
	}()
	var dataResult interface{}
	dataResult, preq.err = pc.App.RunHandler(preq)
	if preq.err != nil || preq.isDone {
		return
	}
	if dataResult != nil {
		jsonData, err := dashutil.MarshalJson(dataResult)
		if err != nil {
			preq.err = dasherr.JsonMarshalErr("handler-return-value", err)
			return
		}
		rrAction := &dashproto.RRAction{
			Ts:         dashutil.Ts(),
			ActionType: "setdata",
			Selector:   RtnSetDataPath,
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
