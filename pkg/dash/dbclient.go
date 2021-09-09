package dash

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// must be divisible by 3 (for base64 encoding)
const blobReadSize = 3 * 340 * 1024

const grpcServerPath = "/grpc-server"
const mbConst = 1000000

const stdGrpcTimeout = 10 * time.Second
const streamGrpcTimeout = 0

const maxBlobBytes = 5000000

const (
	mdConnIdKey        = "dashborg-connid"
	mdClientVersionKey = "dashborg-clientversion"
)

const (
	consoleHostProd = "console.dashborg.net"
	consoleHostDev  = "console.dashborg-dev.com:8080"
)

var NotConnectedErr = dasherr.ErrWithCodeStr(dasherr.ErrCodeNotConnected, "DashborgCloudClient is not Connected")

type accInfoType struct {
	AccType         string  `json:"acctype"`
	AccName         string  `json:"accname"`
	AccCName        string  `json:"acccname"`
	AccJWTEnabled   bool    `json:"accjwtenabled"`
	NewAccount      bool    `json:"newaccount"`
	BlobSizeLimitMB float64 `json:"blobsizelimitmb"`
	HtmlSizeLimitMB float64 `json:"htmlsizelimitmb"`
}

type DashCloudClient struct {
	Lock      *sync.Mutex
	StartTime time.Time
	ProcRunId string
	Config    *Config
	Conn      *grpc.ClientConn
	DBService dashproto.DashborgServiceClient
	ConnId    *atomic.Value
	LinkRtMap map[string]LinkRuntime
	DoneCh    chan bool
	PermErr   bool
	ExitErr   error
	AccInfo   accInfoType
}

func makeCloudClient(config *Config) *DashCloudClient {
	rtn := &DashCloudClient{
		Lock:      &sync.Mutex{},
		StartTime: time.Now(),
		ProcRunId: uuid.New().String(),
		Config:    config,
		ConnId:    &atomic.Value{},
		LinkRtMap: make(map[string]LinkRuntime),
		DoneCh:    make(chan bool),
	}
	rtn.ConnId.Store("")
	return rtn
}

type grpcConfig struct {
	GrpcServer string `json:"grpcserver"`
	GrpcPort   int    `json:"grpcport"`
}

type grpcServerRtn struct {
	Success bool       `json:"success"`
	Error   string     `json:"error"`
	Data    grpcConfig `json:"data"`
}

func (pc *DashCloudClient) getGrpcServer() (*grpcConfig, error) {
	urlVal := fmt.Sprintf("https://%s%s?accid=%s", pc.Config.DashborgConsoleHost, grpcServerPath, pc.Config.AccId)
	resp, err := http.Get(urlVal)
	if err != nil {
		return nil, fmt.Errorf("Cannot get gRPC Server Host: %w", err)
	}
	defer resp.Body.Close()
	bodyContent, err := ioutil.ReadAll(resp.Body)
	var grpcRtn grpcServerRtn
	err = json.Unmarshal(bodyContent, &grpcRtn)
	if err != nil {
		return nil, fmt.Errorf("Cannot get gRPC Server Host (decoding response): %w", err)
	}
	if !grpcRtn.Success {
		return nil, fmt.Errorf("Cannot get gRPC Server Host (error response): %s", grpcRtn.Error)
	}
	if grpcRtn.Data.GrpcServer == "" || grpcRtn.Data.GrpcPort == 0 {
		return nil, fmt.Errorf("Cannot get gRPC Server Host (bad response)")
	}
	return &grpcRtn.Data, nil
}

func (pc *DashCloudClient) startClient() error {
	if pc.Config.DashborgSrvHost == "" {
		grpcConfig, err := pc.getGrpcServer()
		if err != nil {
			pc.logV("DashborgCloudClient error starting: %v\n", err)
			return err
		}
		pc.Config.DashborgSrvHost = grpcConfig.GrpcServer
		pc.Config.DashborgSrvPort = grpcConfig.GrpcPort
		if pc.Config.Verbose {
			pc.log("Dashborg Using gRPC host %s:%d\n", pc.Config.DashborgSrvHost, pc.Config.DashborgSrvPort)
		}
	}
	err := pc.connectGrpc()
	if err != nil {
		pc.logV("DashborgCloudClient ERROR connecting gRPC client: %v\n", err)
	}
	if pc.Config.Verbose {
		pc.log("Dashborg Initialized CloudClient AccId:%s Zone:%s ProcName:%s ProcRunId:%s\n", pc.Config.AccId, pc.Config.ZoneName, pc.Config.ProcName, pc.ProcRunId)
	}
	if pc.Config.ShutdownCh != nil {
		go func() {
			<-pc.Config.ShutdownCh
			pc.externalShutdown()
		}()
	}
	err = pc.sendConnectClientMessage(false)
	if err != nil && !dasherr.CanRetry(err) {
		pc.setExitError(err)
		return err
	}
	go pc.runRequestStreamLoop()
	return nil
}

func (pc *DashCloudClient) ctxWithMd(timeout time.Duration) (context.Context, context.CancelFunc) {
	var ctx context.Context
	var cancelFn context.CancelFunc
	if timeout == 0 {
		ctx = context.Background()
		cancelFn = func() {}
	} else {
		ctx, cancelFn = context.WithTimeout(context.Background(), timeout)
	}
	connId := pc.ConnId.Load().(string)
	ctx = metadata.AppendToOutgoingContext(ctx, mdConnIdKey, connId, mdClientVersionKey, ClientVersion)
	return ctx, cancelFn
}

func (pc *DashCloudClient) externalShutdown() {
	if pc.Conn == nil {
		pc.logV("DashborgCloudClient ERROR shutting down, gRPC connection is not initialized\n")
		return
	}
	pc.setExitError(fmt.Errorf("ShutdownCh channel closed"))
	err := pc.Conn.Close()
	if err != nil {
		pc.logV("DashborgCloudClient ERROR closing gRPC connection: %v\n", err)
	}
}

func makeHostData() map[string]string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	hostData := map[string]string{
		"HostName": hostname,
		"Pid":      strconv.Itoa(os.Getpid()),
	}
	return hostData
}

func (pc *DashCloudClient) sendConnectClientMessage(isReconnect bool) error {
	// only allow one proc message at a time (synchronize)
	hostData := makeHostData()
	m := &dashproto.ConnectClientMessage{
		Ts:        dashutil.Ts(),
		ProcRunId: pc.ProcRunId,
		AccId:     pc.Config.AccId,
		ZoneName:  pc.Config.ZoneName,
		AnonAcc:   pc.Config.AnonAcc,
		ProcName:  pc.Config.ProcName,
		ProcTags:  pc.Config.ProcTags,
		HostData:  hostData,
		StartTs:   dashutil.DashTime(pc.StartTime),
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.ConnectClient(ctx, m)
	dashErr := pc.handleStatusErrors("ConnectClient", resp, respErr, true)
	var accInfo accInfoType
	if dashErr == nil {
		jsonErr := json.Unmarshal([]byte(resp.AccInfoJson), &accInfo)
		if jsonErr != nil {
			dashErr = dasherr.JsonUnmarshalErr("AccInfo", jsonErr)
		}
		if accInfo.AccType == "" {
			dashErr = dasherr.JsonUnmarshalErr("AccInfo", fmt.Errorf("No AccType in AccInfo"))
		}
	}
	if dashErr != nil {
		pc.ConnId.Store("")
		if !dasherr.CanRetry(dashErr) {
			pc.Lock.Lock()
			pc.PermErr = true
			pc.Lock.Unlock()
		}
		return dashErr
	}
	pc.ConnId.Store(resp.ConnId)
	pc.Lock.Lock()
	pc.AccInfo = accInfo
	pc.Lock.Unlock()
	if !isReconnect {
		if accInfo.NewAccount {
			pc.printNewAccMessage()
		} else if accInfo.AccType == "anon" {
			pc.printAnonAccMessage()
		}
		if pc.Config.Verbose {
			pc.log("DashborgCloudClient Connected, AccId:%s Zone:%s ConnId:%s AccType:%v\n", pc.Config.AccId, pc.Config.ZoneName, resp.ConnId, accInfo.AccType)
		} else {
			pc.log("DashborgCloudClient Connected, AccId:%s Zone:%s\n", pc.Config.AccId, pc.Config.ZoneName)
		}
	} else {
		if pc.Config.Verbose {
			pc.log("DashborgCloudClient ReConnected, AccId:%s Zone:%s ConnId:%s\n", pc.Config.AccId, pc.Config.ZoneName, resp.ConnId)
		}
		pc.reconnectLinks()
	}
	return nil
}

func (pc *DashCloudClient) getLinkPaths() []string {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	var paths []string
	for path, _ := range pc.LinkRtMap {
		paths = append(paths, path)
	}
	return paths
}

func (pc *DashCloudClient) connectLinkRpc(path string) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	pathId, err := parsePathToPathId(path, false)
	if err != nil {
		return err
	}
	m := &dashproto.ConnectLinkMessage{
		Ts:   dashutil.Ts(),
		Path: pathId,
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.ConnectLink(ctx, m)
	dashErr := pc.handleStatusErrors(fmt.Sprintf("ConnectLink(%s)", path), resp, respErr, false)
	if dashErr != nil {
		return dashErr
	}
	return nil
}

func parsePathToPathId(path string, allowFrag bool) (*dashproto.PathId, error) {
	var err error
	rtn := &dashproto.PathId{}
	rtn.PathNs, rtn.Path, rtn.PathFrag, err = dashutil.ParseFullPath(path, allowFrag)
	if err != nil {
		return nil, dasherr.ValidateErr(err)
	}
	return rtn, nil
}

func (pc *DashCloudClient) reconnectLinks() {
	linkPaths := pc.getLinkPaths()
	for _, linkPath := range linkPaths {
		err := pc.connectLinkRpc(linkPath)
		if err != nil {
			pc.log("DashborgCloudClient %v\n", err)
		} else {
			pc.logV("DashborgCloudClient reconnected link:%s\n", linkPath)
		}
	}
}

func (pc *DashCloudClient) printNewAccMessage() {
	pc.log("Welcome to Dashborg!  Your new account has been provisioned.  AccId:%s\n", pc.Config.AccId)
	pc.log("You are currently using a free version of the Dashborg Service.\n")
	pc.log("Your use of this service is subject to the Dashborg Terms of Service - https://www.dashborg.net/static/tos.html\n")
}

func (pc *DashCloudClient) printAnonAccMessage() {
	pc.log("You are currently using a free version of the Dashborg Service.\n")
	pc.log("Your use of this service is subject to the Dashborg Terms of Service - https://www.dashborg.net/static/tos.html\n")
}

func (pc *DashCloudClient) handleStatusErrors(fnName string, resp interface{}, respErr error, forceLog bool) error {
	var rtnErr error
	if respErr != nil {
		rtnErr = dasherr.RpcErr(fnName, respErr)
	} else {
		respV := reflect.ValueOf(resp).Elem()
		rtnStatus := respV.FieldByName("Status").Interface().(*dashproto.RtnStatus)
		rtnErr = dasherr.FromRtnStatus(fnName, rtnStatus)
	}
	if rtnErr == nil {
		return nil
	}
	if forceLog || pc.Config.Verbose {
		pc.log("DashborgCloudClient %v\n", rtnErr)
	}
	pc.explainLimit(pc.AccInfo.AccType, rtnErr.Error())
	return rtnErr
}

func (pc *DashCloudClient) connectGrpc() error {
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

func (pc *DashCloudClient) unlinkRuntime(path string) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	delete(pc.LinkRtMap, path)
}

func (pc *DashCloudClient) connectLinkRuntime(path string, rt LinkRuntime) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	pc.LinkRtMap[path] = rt
}

func (pc *DashCloudClient) removePath(path string) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	pathId, err := parsePathToPathId(path, false)
	if err != nil {
		return err
	}
	m := &dashproto.RemovePathMessage{
		Ts:   dashutil.Ts(),
		Path: pathId,
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.RemovePath(ctx, m)
	dashErr := pc.handleStatusErrors(fmt.Sprintf("RemovePath(%s)", path), resp, respErr, true)
	if dashErr != nil {
		return dashErr
	}
	pc.log("DashborgCloudClient removed path %s\n", path)
	return nil
}

func (pc *DashCloudClient) fileInfo(path string, dirOpts *DirOpts) ([]*FileInfo, error) {
	if !pc.IsConnected() {
		return nil, NotConnectedErr
	}
	pathId, err := parsePathToPathId(path, false)
	if err != nil {
		return nil, err
	}
	m := &dashproto.FileInfoMessage{
		Ts:   dashutil.Ts(),
		Path: pathId,
	}
	if dirOpts != nil {
		jsonStr, err := dashutil.MarshalJson(dirOpts)
		if err != nil {
			return nil, dasherr.JsonMarshalErr("DirOpts", err)
		}
		m.DirOptsJson = jsonStr
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.FileInfo(ctx, m)
	dashErr := pc.handleStatusErrors(fmt.Sprintf("FileInfo(%s)", path), resp, respErr, false)
	if dashErr != nil {
		return nil, dashErr
	}
	if resp.FileInfoJson == "" {
		return nil, nil
	}
	var rtn []*FileInfo
	err = json.Unmarshal([]byte(resp.FileInfoJson), &rtn)
	if err != nil {
		return nil, dasherr.JsonUnmarshalErr("FileInfoJson", err)
	}
	return rtn, nil
}

func (pc *DashCloudClient) runRequestStreamLoop() {
	defer close(pc.DoneCh)

	w := &expoWait{CloudClient: pc}
	for {
		state := pc.Conn.GetState()
		if state == connectivity.Shutdown {
			pc.log("DashborgCloudClient RunRequestStreamLoop exiting - Conn Shutdown\n")
			pc.setExitError(fmt.Errorf("gRPC Connection Shutdown"))
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
			err := pc.sendConnectClientMessage(true)
			if err != nil && !dasherr.CanRetry(err) {
				pc.log("DashborgCloudClient RunRequestStreamLoop exiting - Permanent Error: %v\n", err)
				pc.setExitError(err)
				break
			}
			if err != nil {
				continue
			}
		}
		ranOk, errCode := pc.runRequestStream()
		if ranOk {
			w.Reset()
		}
		if errCode == dasherr.ErrCodeBadConnId {
			pc.ConnId.Store("")
			continue
		}
		w.ForceWait = true
	}
}

func (pc *DashCloudClient) sendErrResponse(reqMsg *dashproto.RequestMessage, errMsg string) {
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
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.SendResponse(ctx, m)
	dashErr := pc.handleStatusErrors("SendResponse", resp, respErr, false)
	if dashErr != nil {
		pc.logV("Error sending Error Response: %v\n", dashErr)
	}
}

func (pc *DashCloudClient) runRequestStream() (bool, dasherr.ErrCode) {
	m := &dashproto.RequestStreamMessage{Ts: dashutil.Ts()}
	pc.logV("Dashborg gRPC RequestStream starting\n")
	ctx, cancelFn := pc.ctxWithMd(streamGrpcTimeout)
	defer cancelFn()
	reqStreamClient, err := pc.DBService.RequestStream(ctx, m)
	if err != nil {
		pc.log("Dashborg Error setting up gRPC RequestStream: %v\n", err)
		return false, dasherr.ErrCodeRpc
	}
	startTime := time.Now()
	var reqCounter int64
	var endingErrCode dasherr.ErrCode
	for {
		reqMsg, err := reqStreamClient.Recv()
		if err == io.EOF {
			pc.logV("Dashborg gRPC RequestStream done: EOF\n")
			endingErrCode = dasherr.ErrCodeEof
			break
		}
		if err != nil {
			pc.logV("Dashborg %v\n", dasherr.RpcErr("RequestStream", err))
			endingErrCode = dasherr.ErrCodeRpc
			break
		}
		if reqMsg.Status != nil {
			dashErr := dasherr.FromRtnStatus("RequestStream", reqMsg.Status)
			if dashErr != nil {
				pc.logV("Dashborg %v\n", dashErr)
				endingErrCode = dasherr.GetErrCode(dashErr)
				break
			}
		}
		pc.logV("Dashborg gRPC got request %s\n", requestMsgStr(reqMsg))
		go func() {
			atomic.AddInt64(&reqCounter, 1)
			timeoutMs := reqMsg.TimeoutMs
			if timeoutMs == 0 || timeoutMs > 60000 {
				timeoutMs = 60000
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
			defer cancel()
			if reqMsg.Path == nil {
				pc.sendErrResponse(reqMsg, "Bad Request - No Path")
				return
			}
			if reqMsg.RequestType == "path" {
				fullPath := pathWithNs(reqMsg.Path, false)
				pc.Lock.Lock()
				runtimeVal := pc.LinkRtMap[fullPath]
				pc.Lock.Unlock()
				if runtimeVal == nil {
					pc.sendErrResponse(reqMsg, "No Linked Runtime")
					return
				}
				pc.dispatchRtRequest(ctx, runtimeVal, reqMsg)
				return
			} else {
				pc.sendErrResponse(reqMsg, fmt.Sprintf("Invalid RequestType '%s'", reqMsg.RequestType))
				return
			}
		}()
	}
	elapsed := time.Since(startTime)
	return (elapsed >= 5*time.Second), endingErrCode
}

func (pc *DashCloudClient) sendPathResponse(preq *AppRequest, rtnVal interface{}, appReq bool) {
	if preq.IsDone() {
		return
	}
	m := &dashproto.SendResponseMessage{
		Ts:          dashutil.Ts(),
		ReqId:       preq.RequestInfo().ReqId,
		RequestType: preq.RequestInfo().RequestType,
		Path: &dashproto.PathId{
			PathNs:   preq.RequestInfo().PathNs,
			Path:     preq.RequestInfo().Path,
			PathFrag: preq.RequestInfo().PathFrag,
		},
		FeClientId:   preq.RequestInfo().FeClientId,
		ResponseDone: true,
	}
	if preq.RequestInfo().AppName != "" {
		m.AppId = &dashproto.AppId{AppName: preq.RequestInfo().AppName}
	}

	defer pc.sendResponseProtoRpc(m)
	rtnErr := preq.GetError()
	if rtnErr != nil {
		m.Err = rtnErr.Error()
		return
	}
	var rtnValRRA []*dashproto.RRAction
	if rtnVal != nil {
		var err error
		rtnValRRA, err = rtnValToRRA(rtnVal)
		if err != nil {
			m.Err = err.Error()
			return
		}
	}
	if appReq {
		m.Actions = preq.getRRA()
	}
	m.Actions = append(m.Actions, rtnValRRA...)
	return
}

func (pc *DashCloudClient) sendAppResponse(preq *AppRequest, rtnVal interface{}) {
	if preq.IsDone() {
		return
	}
	m := &dashproto.SendResponseMessage{
		Ts:          dashutil.Ts(),
		ReqId:       preq.RequestInfo().ReqId,
		AppId:       &dashproto.AppId{AppName: preq.RequestInfo().AppName},
		RequestType: preq.RequestInfo().RequestType,
		Path: &dashproto.PathId{
			PathNs:   preq.RequestInfo().PathNs,
			Path:     preq.RequestInfo().Path,
			PathFrag: preq.RequestInfo().PathFrag,
		},
		FeClientId:   preq.RequestInfo().FeClientId,
		ResponseDone: true,
	}
	rtnErr := preq.GetError()
	if rtnErr != nil {
		m.Err = rtnErr.Error()
	} else if rtnVal != nil {
		rtnValRRA, err := rtnValToRRA(rtnVal)
		if err != nil {
			m.Err = err.Error()
		} else {
			m.Actions = append(preq.getRRA(), rtnValRRA...)
		}
	}
	pc.sendResponseProtoRpc(m)
}

func (pc *DashCloudClient) dispatchRtRequest(ctx context.Context, linkrt LinkRuntime, reqMsg *dashproto.RequestMessage) {
	var rtnVal interface{}
	preq := makeAppRequest(ctx, reqMsg, pc)
	defer func() {
		if panicErr := recover(); panicErr != nil {
			log.Printf("Dashborg PANIC in Handler %s | %v\n", requestMsgStr(reqMsg), panicErr)
			preq.SetError(fmt.Errorf("PANIC in handler %v", panicErr))
			debug.PrintStack()
		}
		pc.sendPathResponse(preq, rtnVal, reqMsg.AppRequest)
	}()
	dataResult, err := linkrt.RunHandler(preq)
	if err != nil {
		preq.SetError(err)
		return
	}
	rtnVal = dataResult
	return
}

func (pc *DashCloudClient) backendPush(appName string, path string, data interface{}) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	pathId, err := parsePathToPathId(path, true)
	if err != nil {
		return err
	}
	m := &dashproto.BackendPushMessage{
		Ts:    dashutil.Ts(),
		AppId: &dashproto.AppId{AppName: appName},
		Path:  pathId,
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.BackendPush(ctx, m)
	dashErr := pc.handleStatusErrors("BackendPush", resp, respErr, false)
	if dashErr != nil {
		return dashErr
	}
	return nil
}

// returns the reason for shutdown (GetExitError())
func (pc *DashCloudClient) WaitForShutdown() error {
	<-pc.DoneCh
	return pc.GetExitError()
}

func (pc *DashCloudClient) setExitError(err error) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	if pc.ExitErr == nil {
		pc.ExitErr = err
	}
}

// Returns nil if client is still running. Returns error (reason for shutdown) if client has stopped.
func (pc *DashCloudClient) GetExitError() error {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	return pc.ExitErr
}

func (pc *DashCloudClient) IsConnected() bool {
	if pc == nil || pc.Config == nil {
		return false
	}
	pc.Lock.Lock()
	defer pc.Lock.Unlock()

	if pc.ExitErr != nil {
		return false
	}
	if pc.Conn == nil {
		return false
	}
	connId := pc.ConnId.Load().(string)
	if connId == "" {
		return false
	}
	return true
}

func (pc *DashCloudClient) getAccHost() string {
	if !pc.IsConnected() {
		panic("DashCloudClient is not connected")
	}
	pc.Lock.Lock()
	defer pc.Lock.Unlock()

	if pc.AccInfo.AccCName != "" {
		if pc.Config.Env != "prod" {
			return fmt.Sprintf("https://%s:8080", pc.AccInfo.AccCName)
		}
		return fmt.Sprintf("https://%s", pc.AccInfo.AccCName)
	}
	accId := pc.Config.AccId
	if pc.Config.Env != "prod" {
		return fmt.Sprintf("https://acc-%s.%s", accId, consoleHostDev)
	}
	return fmt.Sprintf("https://acc-%s.%s", accId, consoleHostProd)
}

// SendResponseProtoRpc is for internal use by the Dashborg AppClient, not to be called by the end user.
func (pc *DashCloudClient) sendResponseProtoRpc(m *dashproto.SendResponseMessage) (int, error) {
	if !pc.IsConnected() {
		return 0, NotConnectedErr
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.SendResponse(ctx, m)
	dashErr := pc.handleStatusErrors("SendResponse", resp, respErr, false)
	if dashErr != nil {
		pc.logV("Error sending response: %v\n", dashErr)
		return 0, dashErr
	}
	return int(resp.NumStreamClients), nil
}

func (pc *DashCloudClient) logV(fmtStr string, args ...interface{}) {
	if pc.Config.Verbose {
		pc.log(fmtStr, args...)
	}
}

func (pc *DashCloudClient) log(fmtStr string, args ...interface{}) {
	if pc.Config.Logger != nil {
		pc.Config.Logger.Printf(fmtStr, args...)
	} else {
		log.Printf(fmtStr, args...)
	}
}

func (pc *DashCloudClient) drainSetPathStream(client dashproto.DashborgService_SetPathClient) {
	client.CloseSend()
	for {
		_, err := client.Recv()
		if err == nil {
			continue
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			pc.logV("drainSetPathStream error: %v\n", err)
			return
		}
	}
}

func validateFileOpts(opts *FileOpts) error {
	if !dashutil.IsFileTypeValid(opts.FileType) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid FileType"))
	}
	if !dashutil.IsRoleListValid(strings.Join(opts.AllowedRoles, ",")) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid AllowedRoles"))
	}
	if opts.Display != "" && !dashutil.IsFileDisplayValid(opts.Display) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid Display"))
	}
	if !dashutil.IsDescriptionValid(opts.Description) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid Description (too long)"))
	}
	if opts.FileType == FileTypeStatic {
		if !dashutil.IsSha256Base64HashValid(opts.Sha256) {
			return dasherr.ValidateErr(fmt.Errorf("Invalid SHA-256 hash value, must be a base64 encoded SHA-256 hash (44 characters), see dashutil.Sha256Base64()"))
		}
		if !dashutil.IsMimeTypeValid(opts.MimeType) {
			return dasherr.ValidateErr(fmt.Errorf("Invalid MimeType"))
		}
		if opts.Size <= 0 {
			return dasherr.ValidateErr(fmt.Errorf("Invalid Size (cannot be 0)"))
		}
	}
	return nil
}

func (pc *DashCloudClient) setRawPath(fullPath string, r io.Reader, fileOpts *FileOpts, linkRt LinkRuntime) error {
	err := pc.setRawPathWrap(fullPath, r, fileOpts, linkRt)
	if err != nil {
		pc.logV("Dashborg SetPath ERROR %s => %s | %v\n", fullPath, shortFileOptsStr(fileOpts), err)
		return err
	}
	pc.logV("Dashborg SetPath %s => %s\n", fullPath, shortFileOptsStr(fileOpts))
	return nil
}

func (pc *DashCloudClient) setRawPathWrap(fullPath string, r io.Reader, fileOpts *FileOpts, linkRt LinkRuntime) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	if fileOpts == nil {
		return dasherr.ValidateErr(fmt.Errorf("SetRawPath cannot receive nil *FileOpts"))
	}
	if !dashutil.IsFullPathValid(fullPath) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid Path '%s'", fullPath))
	}
	pathId, err := parsePathToPathId(fullPath, false)
	if err != nil {
		return err
	}
	if len(fileOpts.AllowedRoles) == 0 {
		fileOpts.AllowedRoles = []string{RoleUser}
	}
	err = validateFileOpts(fileOpts)
	if err != nil {
		return err
	}
	if fileOpts.FileType != FileTypeStatic && r != nil {
		return dasherr.ValidateErr(fmt.Errorf("SetRawPath no io.Reader allowed except for file-type:static"))
	}
	if !fileOpts.IsLinkType() && linkRt != nil {
		return dasherr.ValidateErr(fmt.Errorf("FileType is %s, no dash.LinkRuntime allowed", fileOpts.FileType))
	}
	optsJson, err := dashutil.MarshalJson(fileOpts)
	if err != nil {
		return dasherr.JsonMarshalErr("FileOpts", err)
	}
	ctx, cancelFn := pc.ctxWithMd(streamGrpcTimeout)
	defer cancelFn()
	client, err := pc.DBService.SetPath(ctx)
	if err != nil {
		return err
	}
	defer pc.drainSetPathStream(client)

	m := &dashproto.SetPathMessage{
		Ts:             dashutil.Ts(),
		Path:           pathId,
		HasBody:        (r != nil),
		ConnectRuntime: (linkRt != nil),
		FileOptsJson:   optsJson,
	}
	err = client.Send(m)
	if err != nil {
		return err
	}
	metaResp, respErr := client.Recv()
	dashErr := pc.handleStatusErrors("SetPath", metaResp, respErr, true)
	if dashErr != nil {
		return dashErr
	}
	if metaResp.BlobFound || !m.HasBody {
		if fileOpts.IsLinkType() && linkRt != nil {
			pc.connectLinkRuntime(fullPath, linkRt)
		}
		return nil
	}
	maxBytes := maxBlobBytes
	if int64(maxBytes) > fileOpts.Size {
		maxBytes = int(fileOpts.Size)
	}
	readBuf := make([]byte, maxBytes)
	var lastErr error
	var totalRead int64
	for {
		if r == nil {
			lastErr = dasherr.ValidateErr(fmt.Errorf("Nil Reader passed to SetPath"))
		}
		bytesRead, readErr := io.ReadFull(r, readBuf)
		if bytesRead == 0 && readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			lastErr = readErr
			break
		}
		totalRead += int64(bytesRead)
		if totalRead > fileOpts.Size {
			break
		}
		m := &dashproto.SetPathMessage{
			Ts:           dashutil.Ts(),
			BlobBytesKey: metaResp.BlobBytesKey,
			BlobBytes:    readBuf[0:bytesRead],
		}
		clientErr := client.Send(m)
		if clientErr != nil {
			return clientErr
		}
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if lastErr != nil || totalRead != fileOpts.Size {
		m := &dashproto.SetPathMessage{
			Ts:        dashutil.Ts(),
			ClientErr: true,
		}
		err := client.Send(m)
		if err != nil {
			return err
		}
	}
	if totalRead > fileOpts.Size {
		return dasherr.ValidateErr(fmt.Errorf("Invalid FileOpts.Size, does not match io.Reader.  Reader has more bytes."))
	}
	if totalRead < fileOpts.Size {
		return dasherr.ValidateErr(fmt.Errorf("Invalid FileOpts.Size, does not match io.Reader.  Reader has less bytes. (%d vs %d)", fileOpts.Size, totalRead))
	}
	err = client.CloseSend()
	if err != nil {
		return err
	}
	resp, respErr := client.Recv()
	dashErr = pc.handleStatusErrors("SetPath", resp, respErr, false)
	if dashErr != nil {
		return dashErr
	}
	return nil
}

func (pc *DashCloudClient) FSClient() *DashFSClient {
	return &DashFSClient{pc}
}

func (pc *DashCloudClient) AppClient() *DashAppClient {
	return &DashAppClient{pc}
}

func requestMsgStr(reqMsg *dashproto.RequestMessage) string {
	if reqMsg.Path == nil {
		return fmt.Sprintf("%s://[no-path]", reqMsg.RequestType)
	}
	if reqMsg.AppId != nil {
		return fmt.Sprintf("%s://%s%s", reqMsg.RequestType, reqMsg.AppId.AppName, pathStr(reqMsg.Path))
	}
	return fmt.Sprintf("%s:/%s", reqMsg.RequestType, pathStr(reqMsg.Path))
}

func pathStr(path *dashproto.PathId) string {
	if path == nil || path.Path == "" {
		return "[no-path]"
	}
	nsStr := ""
	if path.PathNs != "" {
		nsStr = "/@" + path.PathNs
	}
	fragStr := ""
	if path.PathFrag != "" {
		fragStr = ":" + path.PathFrag
	}
	return fmt.Sprintf("%s%s%s", nsStr, path.Path, fragStr)
}

func rtnValToRRA(rtnVal interface{}) ([]*dashproto.RRAction, error) {
	if blobRtn, ok := rtnVal.(BlobReturn); ok {
		return blobToRRA(blobRtn.MimeType, blobRtn.Reader)
	}
	if blobRtn, ok := rtnVal.(*BlobReturn); ok {
		return blobToRRA(blobRtn.MimeType, blobRtn.Reader)
	}
	jsonData, err := dashutil.MarshalJson(rtnVal)
	if err != nil {
		return nil, dasherr.JsonMarshalErr("HandlerReturnValue", err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "setdata",
		Selector:   RtnSetDataPath,
		JsonData:   jsonData,
	}
	return []*dashproto.RRAction{rrAction}, nil
}

// convert to streaming
func blobToRRA(mimeType string, reader io.Reader) ([]*dashproto.RRAction, error) {
	if !dashutil.IsMimeTypeValid(mimeType) {
		return nil, fmt.Errorf("Invalid Mime-Type passed to SetBlobData mime-type=%s", mimeType)
	}
	first := true
	var rra []*dashproto.RRAction
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
				Selector:  RtnSetDataPath,
				BlobBytes: buffer[0:n],
			}
			if first {
				rrAction.ActionType = "blob"
				rrAction.BlobMimeType = mimeType
				first = false
			} else {
				rrAction.ActionType = "blobext"
			}
			rra = append(rra, rrAction)
		}
		if err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return rra, nil
}

func shortFileOptsStr(fileOpts *FileOpts) string {
	if fileOpts == nil {
		return "[null]"
	}
	mimeType := ""
	if fileOpts.MimeType != "" {
		mimeType = ":" + fileOpts.MimeType
	}
	return fmt.Sprintf("%s%s", fileOpts.FileType, mimeType)
}

func pathWithNs(p *dashproto.PathId, includeFrag bool) string {
	if p == nil {
		return "[no-path]"
	}
	pathNs := ""
	if p.PathNs != "" {
		pathNs = "/@" + p.PathNs
	}
	if includeFrag {
		pathFrag := ""
		if p.PathFrag != "" {
			pathFrag = ":" + p.PathFrag
		}
		return fmt.Sprintf("%s%s%s", pathNs, p.Path, pathFrag)
	} else {
		return fmt.Sprintf("%s%s", pathNs, p.Path)
	}
}
