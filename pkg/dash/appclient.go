package dash

import (
	"context"
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

type handlerFuncType = func(*AppRequest) (interface{}, error)

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
	SendRequestResponse(req *AppRequest, done bool) (int, error)
	StartStream(appName string, streamOpts StreamOpts, feClientId string) (*AppRequest, string, error)
}

// internal callbacks into DashCloud package.  Not for use by end-user, API subject to change.
type InternalApi interface {
	// StartBareStream(appName string, streamOpts StreamOpts) (*AppRequest, error)
	// BackendPush(appName string, path string, data interface{}) error
	SetBlobData(acfg AppConfig, blob BlobData, r io.Reader) error
	RemoveBlob(acfg AppConfig, blob BlobData) error
	ListBlobs(appName string, appVersion string) ([]BlobData, error)
	SendResponseProtoRpc(m *dashproto.SendResponseMessage) (int, error)
	StartStreamProtoRpc(m *dashproto.StartStreamMessage) (string, error)
	SetRawPath(path string, r io.Reader, fileOpts *FileOpts, rt interface{}) error
	RemovePath(path string) error
	FileInfo(path string, dirOpts *DirOpts) ([]*FileInfo, error)
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
func (pc *appClient) SendRequestResponse(req *AppRequest, done bool) (int, error) {
	if req.isStream() && pc.stream_hasZeroClients(req.info.ReqId) {
		req.clearActions()
		return 0, nil
	}
	m := &dashproto.SendResponseMessage{
		Ts:           dashutil.Ts(),
		ReqId:        req.info.ReqId,
		RequestType:  req.info.RequestType,
		AppId:        &dashproto.AppId{AppName: req.info.AppName},
		Path:         &dashproto.PathId{PathNs: req.info.PathNs, Path: req.info.Path, PathFrag: req.info.PathFrag},
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
		Path:         reqMsg.Path,
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
	if reqMsg.RequestType == "streamclose" {
		pc.stream_serverStop(reqMsg.ReqId)
		return // no response for streamclose
	}
	preq := MakeAppRequest(ctx, reqMsg, pc.Api)
	preq.appClient = pc
	if preq.GetError() != nil {
		preq.Done()
		return
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
			preq.err = dasherr.JsonMarshalErr("HandlerReturnValue", err)
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
