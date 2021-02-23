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
	"runtime/debug"
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

const CLIENT_VERSION = "go-0.0.2"

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

// Starts the Dashborg Client
func StartProcClient(config *Config) {
	config.setupForProcClient()
	client := newProcClient()
	client.Config = config
	err := client.connectGrpc()
	if err != nil {
		log.Printf("Dashborg ERROR connecting gRPC client: %v\n", err)
	}
	log.Printf("Dashborg Initialized Client AccId:%s Zone:%s ProcName:%s ProcRunId:%s\n", config.AccId, config.ZoneName, config.ProcName, client.ProcRunId)
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
		logV("Dashborg gRPC got error request: err=%s\n", reqMsg.Err)
		return
	}
	logV("Dashborg gRPC got request: panel=%s, type=%s, path=%s\n", reqMsg.PanelName, reqMsg.RequestType, reqMsg.Path)
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
	preq.DataJson = reqMsg.JsonData

	var pstate interface{}
	if reqMsg.PanelStateData != "" {
		err := json.Unmarshal([]byte(reqMsg.PanelStateData), &pstate)
		if err != nil {
			preq.Err = fmt.Errorf("Cannot unmarshal PanelStateData: %v", err)
			preq.Done()
			return
		}
	}
	preq.PanelState = pstate
	preq.PanelStateJson = reqMsg.PanelStateData

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
		if !preq.IsAuthenticated() {
			preq.Err = fmt.Errorf("Request is not authenticated")
			preq.Done()
			return
		}
	}

	defer func() {
		if panicErr := recover(); panicErr != nil {
			log.Printf("Dashborg PANIC in Handler %v | %v\n", hkey, panicErr)
			preq.Err = fmt.Errorf("PANIC in handler %v", panicErr)
			debug.PrintStack()
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

func (pc *procClient) sendProcMessage() error {
	// only allow one proc message at a time (synchronize)
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
		log.Printf("Dashborg procclient sendProcMessage error: %v\n", err)
		pc.ConnId.Store("")
		return err
	}
	if !resp.Success {
		log.Printf("Dashborg procclient sendProcMessage error: %s\n", resp.Err)
		pc.ConnId.Store("")
		return errors.New(resp.Err)
	}
	pc.ConnId.Store(resp.ConnId)
	if pc.Config.Verbose {
		log.Printf("procclient sendProcMessage success connid:%s\n", resp.ConnId)
	}
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
		log.Printf("Dashborg procclient RunRequestStreamLoop trying to connect (%0.1fs) %d\n", float64(msWait)/1000, w.WaitTimes)
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
			logV("Dashborg procclient RunRequestStreamLoop exiting -- Conn Shutdown\n")
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
	logV("Dashborg gRPC RequestStream starting\n")
	reqStreamClient, err := pc.DBService.RequestStream(pc.ctxWithMd(), m)
	if err != nil {
		log.Printf("Dashborg Error setting up gRPC RequestStream: %v\n", err)
		return false, EC_UNKNOWN
	}
	startTime := time.Now()
	reqCounter := 0
	var endingErrCode string
	for {
		reqMsg, err := reqStreamClient.Recv()
		if err == io.EOF {
			logV("Dashborg gRPC RequestStream done: EOF\n")
			endingErrCode = EC_EOF
			break
		}
		if err != nil {
			logV("Dashborg gRPC RequestStream ERROR: %v\n", err)
			endingErrCode = EC_UNKNOWN
			break
		}
		if reqMsg.ErrCode == dashproto.ErrorCode_EC_BADCONNID {
			logV("Dashborg gRPC RequestStream BADCONNID\n")
			endingErrCode = EC_BADCONNID
			break
		}
		go func() {
			reqCounter++
			timeoutMs := reqMsg.TimeoutMs
			if timeoutMs == 0 || timeoutMs > 60000 {
				timeoutMs = 60000
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(reqMsg.TimeoutMs)*time.Millisecond)
			defer cancel()
			pc.dispatchRequest(ctx, reqMsg)
		}()
	}
	elapsed := time.Since(startTime)
	return (elapsed >= 5*time.Second), endingErrCode
}

// WaitForClear closes the gRPC connection to the server and shuts down the Dashborg client.
// Usually called at the end of main() using defer.
func WaitForClear() {
	time.Sleep(globalClient.Config.MinClearTimeout)
	err := globalClient.Conn.Close()
	if err != nil {
		logV("Dashborg ERROR closing gRPC connection: %v\n", err)
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
	if pc.ConnId.Load().(string) == "" {
		return
	}
	msg := &dashproto.RegisterHandlerMessage{
		Ts:       dashutil.Ts(),
		Handlers: []*dashproto.HandlerKey{protoHkey},
	}
	resp, err := globalClient.DBService.RegisterHandler(pc.ctxWithMd(), msg)
	if err != nil {
		log.Printf("Dashborg RegisterHandler ERROR-rpc %v\n", err)
		return
	}
	if resp.Err != "" {
		log.Printf("Dashborg RegisterHandler ERROR %v\n", resp.Err)
		return
	}
	logV("Dashborg RegisterHandler %v\n", hkey)
}
