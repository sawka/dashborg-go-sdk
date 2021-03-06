package dashlocal

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var notImplErr error

func init() {
	notImplErr = fmt.Errorf("Not Implemented in LocalServer mode")
}

type LocalClient struct {
	Lock         *sync.Mutex
	Config       *Config
	ConnId       string
	HandlerMap   map[handlerKey]bool
	LocalServer  *localServer
	ReqClient    *reqClient
	LocalReqMap  map[string]*localReq
	StreamClient *streamClient
}

type localReq struct {
	Ctx     context.Context
	DoneCh  chan struct{}
	Actions []*dashproto.RRAction
}

type reqClient struct {
	ReqCh      chan *dashproto.RequestMessage
	ShutdownCh chan struct{}
}

type handlerKey struct {
	PanelName   string
	HandlerType string
	Path        string
}

func MakeLocalClient(config *Config) (*LocalClient, error) {
	rtn := &LocalClient{
		Lock:   &sync.Mutex{},
		Config: config,
	}
	rtn.HandlerMap = make(map[handlerKey]bool)
	rtn.LocalReqMap = make(map[string]*localReq)
	rtn.ReqClient = makeReqClient(config)
	rtn.StreamClient = makeStreamClient(rtn.sendStreamClose)
	go func() {
		startLocalErr := StartLocalServer(config, rtn)
		if startLocalErr != nil {
			log.Printf("Dashborg ERROR starting local server: %v\n", startLocalErr)
		}
	}()
	return rtn, nil
}

func makeReqClient(config *Config) *reqClient {
	rtn := &reqClient{}
	rtn.ReqCh = make(chan *dashproto.RequestMessage, 10)
	rtn.ShutdownCh = config.ShutdownCh
	return rtn
}

func (c *LocalClient) Proc(ctx context.Context, in *dashproto.ProcMessage, opts ...grpc.CallOption) (*dashproto.ProcResponse, error) {
	c.Lock.Lock()
	defer c.Lock.Unlock()

	if c.ConnId == "" {
		c.ConnId = uuid.New().String()
	}
	return &dashproto.ProcResponse{
		Success: true,
		ConnId:  c.ConnId,
	}, nil
}

func (c *LocalClient) SendResponse(ctx context.Context, in *dashproto.SendResponseMessage, opts ...grpc.CallOption) (*dashproto.SendResponseResponse, error) {
	c.Lock.Lock()
	defer c.Lock.Unlock()

	if in.RequestType == "stream" {
		numClients, err := c.StreamClient.recvResponse(in)
		if err != nil {
			return &dashproto.SendResponseResponse{Err: err.Error()}, nil
		}
		return &dashproto.SendResponseResponse{Success: true, NumStreamClients: int32(numClients)}, nil
	}

	lreq := c.LocalReqMap[in.ReqId]
	if lreq == nil {
		return &dashproto.SendResponseResponse{Err: fmt.Sprintf("No open request found for reqid:%s", in.ReqId)}, nil
	}
	if in.Err != "" {
		lreq.Actions = []*dashproto.RRAction{&dashproto.RRAction{Ts: in.Ts, ActionType: "error", Err: in.Err}}
	} else {
		lreq.Actions = append(lreq.Actions, in.Actions...)
	}
	if in.ResponseDone {
		close(lreq.DoneCh)
	}
	return &dashproto.SendResponseResponse{Success: true}, nil
}

func (c *LocalClient) RegisterHandler(ctx context.Context, in *dashproto.RegisterHandlerMessage, opts ...grpc.CallOption) (*dashproto.RegisterHandlerResponse, error) {
	c.Lock.Lock()
	defer c.Lock.Unlock()

	for _, inH := range in.Handlers {
		hkey := handlerKey{PanelName: inH.PanelName, HandlerType: inH.HandlerType, Path: inH.Path}
		c.HandlerMap[hkey] = true
	}
	return &dashproto.RegisterHandlerResponse{Success: true}, nil
}

func (c *LocalClient) RequestStream(ctx context.Context, in *dashproto.RequestStreamMessage, opts ...grpc.CallOption) (dashproto.DashborgService_RequestStreamClient, error) {
	return c.ReqClient, nil
}

func (c *LocalClient) CallDataHandler(ctx context.Context, in *dashproto.CallDataHandlerMessage, opts ...grpc.CallOption) (*dashproto.CallDataHandlerResponse, error) {
	return nil, fmt.Errorf("CallDataHandler not supported in LocalClient")
}

func (c *LocalClient) StartStream(ctx context.Context, in *dashproto.StartStreamMessage, opts ...grpc.CallOption) (*dashproto.StartStreamResponse, error) {
	reqId, err := c.StreamClient.startStream(in.ExistingReqId, in.FeClientId)
	if err != nil {
		return &dashproto.StartStreamResponse{Err: err.Error()}, nil
	}
	return &dashproto.StartStreamResponse{Success: true, ReqId: reqId}, nil
}

func (c *LocalClient) BackendPush(ctx context.Context, in *dashproto.BackendPushMessage, opts ...grpc.CallOption) (*dashproto.BackendPushResponse, error) {
	rr := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "backendpush",
		Selector:   in.Path,
	}
	c.StreamClient.backendPush(in.PanelName, rr)
	return &dashproto.BackendPushResponse{Success: true}, nil
}

func (c *LocalClient) ReflectZone(ctx context.Context, in *dashproto.ReflectZoneMessage, opts ...grpc.CallOption) (*dashproto.ReflectZoneResponse, error) {
	return nil, fmt.Errorf("ReflectZone not supported in LocalClient")
}

////////////////////////

func (rc *reqClient) Recv() (*dashproto.RequestMessage, error) {
	select {
	case m := <-rc.ReqCh:
		return m, nil

	case <-rc.ShutdownCh:
		return nil, io.EOF
	}
}

func (rc *reqClient) Header() (metadata.MD, error) {
	return nil, notImplErr
}

func (rc *reqClient) Trailer() metadata.MD {
	return nil
}

func (rc *reqClient) CloseSend() error {
	return notImplErr
}

func (rc *reqClient) Context() context.Context {
	return nil
}

func (rc *reqClient) SendMsg(m interface{}) error {
	return notImplErr
}

func (rc *reqClient) RecvMsg(m interface{}) error {
	return notImplErr
}

//////////////////////////

func (c *LocalClient) DispatchLocalRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) ([]*dashproto.RRAction, error) {
	select {
	case c.ReqClient.ReqCh <- reqMsg:
		break
	default:
		return nil, fmt.Errorf("Dashborg Cannot Dispatch Request, Queue Full")
	}
	reqId := reqMsg.ReqId
	req := &localReq{Ctx: ctx, DoneCh: make(chan struct{})}
	c.Lock.Lock()
	c.LocalReqMap[reqId] = req
	c.Lock.Unlock()

	select {
	case <-ctx.Done():
		c.unlinkReq(reqId)
		return nil, fmt.Errorf("Context canceled")

	case <-req.DoneCh:
		c.unlinkReq(reqId)
		break
	}
	return req.Actions, nil
}

func (c *LocalClient) unlinkReq(reqId string) {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	delete(c.LocalReqMap, reqId)
}

func (c *LocalClient) DrainLocalFeStream(ctx context.Context, feClientId string, timeout time.Duration, pushPanel string) ([]*dashproto.RRAction, []string, error) {
	return c.StreamClient.drainFeStream(ctx, feClientId, timeout, pushPanel)
}

func (c *LocalClient) StopStream(reqId string, feClientId string) error {
	c.StreamClient.endReqStream_client(reqId, feClientId)
	return nil
}

func (c *LocalClient) sendStreamClose(reqId string) {
	req := &dashproto.RequestMessage{
		Ts:          dashutil.Ts(),
		ReqId:       reqId,
		RequestType: "streamclose",
	}
	go func() {
		c.DispatchLocalRequest(context.Background(), req)
	}()
}
