package dash

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

const EC_EOF = "EOF"
const EC_UNKNOWN = "UNKNOWN"
const EC_BADCONNID = "BADCONNID"
const EC_ACCACCESS = "ACCACCESS"
const EC_NOHANDLER = "NOHANDLER"
const EC_UNAVAILABLE = "UNAVAILABLE"

const CLIENT_VERSION = "go-0.0.1"

var globalClient *procClient

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

type procClient struct {
	CVar       *sync.Cond
	StartTs    int64
	ProcRunId  string
	Config     *Config
	Conn       *grpc.ClientConn
	DBService  dashproto.DashborgServiceClient
	HandlerMap map[handlerKey]handlerVal
	ConnId     *atomic.Value
}

func newProcClient() *procClient {
	rtn := &procClient{}
	rtn.CVar = sync.NewCond(&sync.Mutex{})
	rtn.StartTs = dashutil.Ts()
	rtn.ProcRunId = uuid.New().String()
	rtn.HandlerMap = make(map[handlerKey]handlerVal)
	rtn.ConnId = &atomic.Value{}
	rtn.ConnId.Store("")
	return rtn
}

func (pc *procClient) copyHandlerKeys() []*dashproto.HandlerKey {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	rtn := make([]*dashproto.HandlerKey, 0, len(pc.HandlerMap))
	for hk, _ := range pc.HandlerMap {
		hkCopy := &dashproto.HandlerKey{
			PanelName:   hk.PanelName,
			HandlerType: hk.HandlerType,
			Path:        hk.Path,
		}
		rtn = append(rtn, hkCopy)
	}
	return rtn
}

func StartProcClient(config *Config) {
	config.setupForProcClient()
	client := newProcClient()
	client.Config = config
	// TODO keepalive
	err := client.connectGrpc()
	if err != nil {
		log.Printf("ERROR connecting gRPC client: %v\n", err)
	}
	log.Printf("Dashborg Initialized Client AccId:%s Zone:%s ProcName:%s ProcRunId:%s\n", config.AccId, config.ZoneName, config.ProcName, client.ProcRunId)
	// TODO error handling, run async, and hold other requests until this is done
	client.sendProcMessage()
	go client.runRequestStreamLoop()
	globalClient = client
}

func (pc *procClient) registerHandlerFn(hkey handlerKey, protoHKey *dashproto.HandlerKey, handlerFn handlerFuncType) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	pc.HandlerMap[hkey] = handlerVal{HandlerFn: handlerFn, ProtoHKey: protoHKey}
}

func (pc *procClient) connectGrpc() error {
	addr := pc.Config.DashborgSrvHost + ":" + strconv.Itoa(pc.Config.DashborgSrvPort)
	backoffConfig := backoff.Config{
		BaseDelay:  1.0 * time.Second,
		Multiplier: 1.6,
		Jitter:     0.2,
		MaxDelay:   60 * time.Second,
	}
	connectParams := grpc.ConnectParams{MinConnectTimeout: time.Second, Backoff: backoffConfig}
	keepaliveParams := keepalive.ClientParameters{Time: 10 * time.Second, Timeout: 5 * time.Second, PermitWithoutStream: true}
	clientCert, err := tls.LoadX509KeyPair(pc.Config.CertFileName, pc.Config.KeyFileName)
	if err != nil {
		return fmt.Errorf("Cannot load keypair key:%s cert:%s err:%w", pc.Config.KeyFileName, pc.Config.CertFileName, err)
	}
	tlsConfig := &tls.Config{
		MinVersion:               tls.VersionTLS13,
		CurvePreferences:         []tls.CurveID{tls.CurveP384},
		PreferServerCipherSuites: true,
		InsecureSkipVerify:       true,
		Certificates:             []tls.Certificate{clientCert},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		},
	}
	tlsCreds := credentials.NewTLS(tlsConfig)
	conn, err := grpc.Dial(
		addr,
		grpc.WithConnectParams(connectParams),
		grpc.WithKeepaliveParams(keepaliveParams),
		grpc.WithTransportCredentials(tlsCreds),
	)
	pc.Conn = conn
	pc.DBService = dashproto.NewDashborgServiceClient(conn)
	return err
}

func marshalJson(val interface{}) (string, error) {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(val)
	if err != nil {
		return "", err
	}
	return jsonBuf.String(), nil
}

func (pc *procClient) sendRequestResponse(req *PanelRequest, done bool) error {
	m := &dashproto.SendResponseMessage{
		Ts:           dashutil.Ts(),
		ReqId:        req.ReqId,
		RequestType:  req.RequestType,
		PanelName:    req.PanelName,
		FeClientId:   req.FeClientId,
		ResponseDone: done,
	}
	if req.Err != nil {
		m.Err = req.Err.Error()
	}

	req.Lock.Lock()
	m.Actions = req.RRActions
	req.RRActions = nil
	req.Lock.Unlock()

	if pc.ConnId.Load().(string) == "" {
		return fmt.Errorf("No Active ConnId")
	}
	resp, err := pc.DBService.SendResponse(pc.ctxWithMd(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	return nil
}

func (pc *procClient) dispatchRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) {
	if reqMsg.Err != "" {
		log.Printf("gRPC got error request: err=%s\n", reqMsg.Err)
		return
	}
	log.Printf("gRPC got request: panel=%s, type=%s, path=%s\n", reqMsg.PanelName, reqMsg.RequestType, reqMsg.Path)
	preq := &PanelRequest{
		Ctx:         ctx,
		Lock:        &sync.Mutex{},
		PanelName:   reqMsg.PanelName,
		ReqId:       reqMsg.ReqId,
		RequestType: reqMsg.RequestType,
		FeClientId:  reqMsg.FeClientId,
		Path:        reqMsg.Path,
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
	case "panel":
		hkey.HandlerType = "panel"
	case "streamopen":
		fallthrough
	case "streamclose":
		hkey.HandlerType = "stream"
	default:
		preq.Err = fmt.Errorf("Invalid RequestMessage.RequestType [%s]", reqMsg.RequestType)
		preq.Done()
		return
	}
	pc.CVar.L.Lock()
	hval, ok := pc.HandlerMap[hkey]
	pc.CVar.L.Unlock()
	if !ok {
		preq.Err = fmt.Errorf("No Handler found for path=%s", reqMsg.Path)
		preq.Done()
		return
	}

	var data interface{}
	if reqMsg.JsonData != "" {
		err := json.Unmarshal([]byte(reqMsg.JsonData), &data)
		if err != nil {
			preq.Err = fmt.Errorf("Cannot unmarshal JsonData: %v", err)
			preq.Done()
			return
		}
	}
	preq.Data = data

	var model interface{}
	if reqMsg.ModelData != "" {
		err := json.Unmarshal([]byte(reqMsg.ModelData), &model)
		if err != nil {
			preq.Err = fmt.Errorf("Cannot unmarshal ModelData: %v", err)
			preq.Done()
			return
		}
	}
	preq.Model = model

	var authData []*authAtom
	if reqMsg.AuthData != "" {
		err := json.Unmarshal([]byte(reqMsg.AuthData), &authData)
		if err != nil {
			preq.Err = fmt.Errorf("Cannot unmarshal AuthData: %v", err)
			preq.Done()
			return
		}
	}
	preq.AuthData = authData

	// check-auth
	if !preq.isRootReq() {
		if !preq.isAuthenticated() {
			preq.Err = fmt.Errorf("Request is not authenticated")
			preq.Done()
			return
		}
	}

	defer func() {
		if panicErr := recover(); panicErr != nil {
			log.Printf("PANIC in Handler %v | %v\n", hkey, panicErr)
			preq.Err = fmt.Errorf("PANIC in handler %v", panicErr)
		}
		preq.Done()
	}()
	var dataResult interface{}
	dataResult, preq.Err = hval.HandlerFn(preq)
	if hkey.HandlerType == "data" {
		jsonData, err := marshalJson(dataResult)
		if err != nil {
			preq.Err = err
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

// TODO sync, only one procmessage allowed at a time
func (pc *procClient) sendProcMessage() error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	hostData := map[string]string{
		"HostName": hostname,
		"Pid":      strconv.Itoa(os.Getpid()),
	}
	hkeys := pc.copyHandlerKeys()
	m := &dashproto.ProcMessage{
		Ts:            dashutil.Ts(),
		ProcRunId:     pc.ProcRunId,
		AccId:         pc.Config.AccId,
		ZoneName:      pc.Config.ZoneName,
		AnonAcc:       pc.Config.AnonAcc,
		ProcName:      pc.Config.ProcName,
		ProcTags:      pc.Config.ProcTags,
		HostData:      hostData,
		StartTs:       pc.StartTs,
		Handlers:      hkeys,
		ClientVersion: CLIENT_VERSION,
	}
	resp, err := pc.DBService.Proc(pc.ctxWithMd(), m)
	if err != nil {
		log.Printf("procclient sendProcMessage error: %v\n", err)
		pc.ConnId.Store("")
		return err
	}
	if !resp.Success {
		log.Printf("procclient sendProcMessage error: %s\n", resp.Err)
		pc.ConnId.Store("")
		return errors.New(resp.Err)
	}
	pc.ConnId.Store(resp.ConnId)
	log.Printf("procclient sendProcMessage success connid:%s\n", resp.ConnId)
	return nil
}

func (pc *procClient) ctxWithMd() context.Context {
	ctx := context.Background()
	connId := pc.ConnId.Load().(string)
	ctx = metadata.AppendToOutgoingContext(ctx, "dashborg-connid", connId)
	return ctx
}

type expoWait struct {
	ForceWait       bool
	InitialWait     time.Time
	CurWaitDeadline time.Time
	LastOkMs        int64
	WaitTimes       int
}

func (w *expoWait) Wait() bool {
	hasInitialWait := !w.InitialWait.IsZero()
	if w.InitialWait.IsZero() {
		w.InitialWait = time.Now()
	}
	if w.ForceWait || hasInitialWait {
		time.Sleep(1 * time.Second)
		w.WaitTimes++
		w.ForceWait = false
	}
	msWait := int64(time.Since(w.InitialWait)) / int64(time.Millisecond)
	if !hasInitialWait {
		w.LastOkMs = msWait
		return true
	}
	diffWait := msWait - w.LastOkMs
	var rtnOk bool
	switch {
	case msWait < 4000:
		w.LastOkMs = msWait
		rtnOk = true

	case msWait < 60000 && diffWait > 4800:
		w.LastOkMs = msWait
		rtnOk = true

	case diffWait > 29500:
		w.LastOkMs = msWait
		rtnOk = true
	}
	if rtnOk {
		log.Printf("procclient RunRequestStreamLoop trying to connect (%0.1fs) %d\n", float64(msWait)/1000, w.WaitTimes)
	}
	return rtnOk
}

func (w *expoWait) Reset() {
	*w = expoWait{}
}

func (pc *procClient) runRequestStreamLoop() {
	w := &expoWait{}
	for {
		state := pc.Conn.GetState()
		if state == connectivity.Shutdown {
			log.Printf("procclient RunRequestStreamLoop exiting -- Conn Shutdown\n")
			break
		}
		if state == connectivity.Connecting || state == connectivity.TransientFailure {
			time.Sleep(1 * time.Second)
			w.Reset()
			continue
		}
		okWait := w.Wait()
		if !okWait {
			continue
		}
		if pc.ConnId.Load().(string) == "" {
			err := pc.sendProcMessage()
			if err != nil {
				continue
			}
		}
		ranOk, ec := pc.runRequestStream()
		if ranOk {
			w.Reset()
		}
		if ec == EC_BADCONNID {
			pc.ConnId.Store("")
			continue
		}
		w.ForceWait = true
	}
}

func (pc *procClient) runRequestStream() (bool, string) {
	m := &dashproto.RequestStreamMessage{Ts: dashutil.Ts()}
	log.Printf("gRPC RequestStream starting\n")
	reqStreamClient, err := pc.DBService.RequestStream(pc.ctxWithMd(), m)
	if err != nil {
		// TODO retry
		log.Printf("Error setting up gRPC RequestStream: %v\n", err)
		return false, EC_UNKNOWN
	}
	startTime := time.Now()
	reqCounter := 0
	var endingErrCode string
	for {
		reqMsg, err := reqStreamClient.Recv()
		// log.Printf("rtn from req-stream %v | %v\n", reqMsg, err)
		if err == io.EOF {
			log.Printf("gRPC RequestStream done: EOF\n")
			endingErrCode = EC_EOF
			break
		}
		if err != nil {
			log.Printf("gRPC RequestStream ERROR: %v\n", err)
			endingErrCode = EC_UNKNOWN
			break
		}
		if reqMsg.ErrCode == dashproto.ErrorCode_EC_BADCONNID {
			log.Printf("gRPC RequestStream BADCONNID\n")
			endingErrCode = EC_BADCONNID
			break
		}
		go func() {
			reqCounter++
			ctx := context.Background()
			if reqMsg.TimeoutMs > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(reqMsg.TimeoutMs)*time.Millisecond)
				defer cancel()
			}
			pc.dispatchRequest(ctx, reqMsg)
		}()
	}
	elapsed := time.Since(startTime)
	return (elapsed >= 5*time.Second), endingErrCode
}

func WaitForClear() {
	time.Sleep(globalClient.Config.MinClearTimeout)
	err := globalClient.Conn.Close()
	if err != nil {
		log.Printf("ERROR closing gRPC connection: %v\n", err)
	}
}

// no error returned, locally registers if cannot connect
func (pc *procClient) registerHandler(protoHkey *dashproto.HandlerKey, handlerFn handlerFuncType) {
	hkey := handlerKey{
		PanelName:   protoHkey.PanelName,
		HandlerType: protoHkey.HandlerType,
		Path:        protoHkey.Path,
	}
	pc.registerHandlerFn(hkey, protoHkey, handlerFn)

	// TODO what to do on error?
	// TODO retry/flag errors
	if pc.ConnId.Load().(string) == "" {
		return
	}
	msg := &dashproto.RegisterHandlerMessage{
		Ts:       dashutil.Ts(),
		Handlers: []*dashproto.HandlerKey{protoHkey},
	}
	resp, err := globalClient.DBService.RegisterHandler(pc.ctxWithMd(), msg)
	if err != nil {
		log.Printf("RegisterHandler ERROR-rpc %v\n", err)
		return
	}
	if resp.Err != "" {
		log.Printf("RegisterHandler ERROR %v\n", resp.Err)
		return
	}
	log.Printf("RegisterHandler %v success\n", hkey)
}
