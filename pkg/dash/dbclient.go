package dash

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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

const maxRRABlobSize = 3 * 1024 * 1024 // 3M

const grpcServerPath = "/grpc-server"
const mbConst = 1000000
const uploadTimeout = 5 * time.Minute

const stdGrpcTimeout = 10 * time.Second
const streamGrpcTimeout = 0

const maxBlobBytes = 5000000

const (
	mdConnIdKey        = "dashborg-connid"
	mdClientVersionKey = "dashborg-clientversion"
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
	urlVal := fmt.Sprintf("https://%s%s?accid=%s", pc.Config.ConsoleHost, grpcServerPath, pc.Config.AccId)
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
	if pc.Config.GrpcHost == "" {
		grpcConfig, err := pc.getGrpcServer()
		if err != nil {
			pc.logV("DashborgCloudClient error starting: %v\n", err)
			return err
		}
		pc.Config.GrpcHost = grpcConfig.GrpcServer
		pc.Config.GrpcPort = grpcConfig.GrpcPort
		if pc.Config.Verbose {
			pc.log("Dashborg Using gRPC host %s:%d\n", pc.Config.GrpcHost, pc.Config.GrpcPort)
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
		ProcIKey:  pc.Config.ProcIKey,
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
	err := dashutil.ValidateFullPath(path, false)
	if err != nil {
		return dasherr.ValidateErr(err)
	}
	m := &dashproto.ConnectLinkMessage{
		Ts:   dashutil.Ts(),
		Path: path,
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

func (pc *DashCloudClient) reconnectLinks() {
	linkPaths := pc.getLinkPaths()
	for _, linkPath := range linkPaths {
		err := pc.connectLinkRpc(linkPath)
		if err != nil {
			pc.log("DashborgCloudClient %v\n", err)
		} else {
			pc.logV("DashborgCloudClient ReConnected link:%s\n", dashutil.SimplifyPath(linkPath, nil))
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
	addr := pc.Config.GrpcHost + ":" + strconv.Itoa(pc.Config.GrpcPort)
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

func (pc *DashCloudClient) removeAppPath(appName string) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	if !dashutil.IsAppNameValid(appName) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid app name"))
	}
	appPath := AppPathFromName(appName)
	m := &dashproto.RemovePathMessage{
		Ts:            dashutil.Ts(),
		Path:          appPath,
		RemoveFullApp: true,
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.RemovePath(ctx, m)
	dashErr := pc.handleStatusErrors(fmt.Sprintf("RemoveApp(%s)", appName), resp, respErr, true)
	if dashErr != nil {
		return dashErr
	}
	pc.log("DashborgCloudClient removed app %s\n", appName)
	return nil
}

func (pc *DashCloudClient) removePath(path string) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	err := dashutil.ValidateFullPath(path, false)
	if err != nil {
		return dasherr.ValidateErr(err)
	}
	m := &dashproto.RemovePathMessage{
		Ts:   dashutil.Ts(),
		Path: path,
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

func (pc *DashCloudClient) fileInfo(path string, dirOpts *DirOpts, rtnContents bool) ([]*FileInfo, []byte, error) {
	if !pc.IsConnected() {
		return nil, nil, NotConnectedErr
	}
	err := dashutil.ValidateFullPath(path, false)
	if err != nil {
		return nil, nil, dasherr.ValidateErr(err)
	}
	m := &dashproto.FileInfoMessage{
		Ts:          dashutil.Ts(),
		Path:        path,
		RtnContents: rtnContents,
	}
	if dirOpts != nil {
		jsonStr, err := dashutil.MarshalJson(dirOpts)
		if err != nil {
			return nil, nil, dasherr.JsonMarshalErr("DirOpts", err)
		}
		m.DirOptsJson = jsonStr
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.FileInfo(ctx, m)
	dashErr := pc.handleStatusErrors(fmt.Sprintf("FileInfo(%s)", path), resp, respErr, false)
	if dashErr != nil {
		return nil, nil, dashErr
	}
	if resp.FileInfoJson == "" {
		return nil, nil, nil
	}
	var rtn []*FileInfo
	err = json.Unmarshal([]byte(resp.FileInfoJson), &rtn)
	if err != nil {
		return nil, nil, dasherr.JsonUnmarshalErr("FileInfoJson", err)
	}
	return rtn, resp.FileContent, nil
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
		Path:         reqMsg.Path,
		FeClientId:   reqMsg.FeClientId,
		ResponseDone: true,
		Err:          &dashproto.ErrorType{Err: errMsg},
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
		pc.logV("Dashborg gRPC request %s\n", requestMsgStr(reqMsg))
		go func() {
			atomic.AddInt64(&reqCounter, 1)
			timeoutMs := reqMsg.TimeoutMs
			if timeoutMs == 0 || timeoutMs > 60000 {
				timeoutMs = 60000
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
			defer cancel()
			if reqMsg.Path == "" {
				pc.sendErrResponse(reqMsg, "Bad Request - No Path")
				return
			}
			if reqMsg.RequestType == "path" {
				fullPath, err := dashutil.PathNoFrag(reqMsg.Path)
				if err != nil {
					pc.sendErrResponse(reqMsg, fmt.Sprintf("Error parsing path: %v", err))
				}
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
		Ts:           dashutil.Ts(),
		ReqId:        preq.RequestInfo().ReqId,
		RequestType:  preq.RequestInfo().RequestType,
		Path:         preq.RequestInfo().Path,
		FeClientId:   preq.RequestInfo().FeClientId,
		ResponseDone: true,
	}
	defer pc.sendResponseProtoRpc(m)
	rtnErr := preq.GetError()
	if rtnErr != nil {
		m.Err = dasherr.AsProtoErr(rtnErr)
		return
	}
	var rtnValRRA []*dashproto.RRAction
	if rtnVal != nil {
		var err error
		rtnValRRA, err = rtnValToRRA(rtnVal)
		if err != nil {
			m.Err = dasherr.AsProtoErr(err)
			return
		}
	}
	if appReq {
		m.Actions = preq.getRRA()
	}
	m.Actions = append(m.Actions, rtnValRRA...)
	return
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
	return fmt.Sprintf("https://acc-%s.%s", accId, pc.Config.ConsoleHost)
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

func (opts *FileOpts) Validate() error {
	if opts == nil {
		return dasherr.ValidateErr(fmt.Errorf("FileOpts is nil"))
	}
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
	if opts.FileType == FileTypeApp && opts.AppConfigJson == "" {
		return dasherr.ValidateErr(fmt.Errorf("FileType 'app' must have AppConfigJson set"))
	}
	if opts.FileType != FileTypeApp && opts.AppConfigJson != "" {
		return dasherr.ValidateErr(fmt.Errorf("FileType '%s' must not have AppConfigJson set", opts.FileType))
	}
	if opts.AppConfigJson != "" {
		if len(opts.AppConfigJson) > dashutil.AppConfigJsonMax {
			return dasherr.ValidateErr(fmt.Errorf("AppConfig too large"))
		}
		var acfg AppConfig
		err := json.Unmarshal([]byte(opts.AppConfigJson), &acfg)
		if err != nil {
			return dasherr.JsonUnmarshalErr("AppConfig", err)
		}
	}
	if opts.MetadataJson != "" {
		if len(opts.MetadataJson) > dashutil.MetadataJsonMax {
			return dasherr.ValidateErr(fmt.Errorf("Metadata too large"))
		}
		var testJson interface{}
		err := json.Unmarshal([]byte(opts.MetadataJson), &testJson)
		if err != nil {
			return dasherr.JsonUnmarshalErr("Metadata", err)
		}
	}
	return nil
}

func (pc *DashCloudClient) setRawPath(fullPath string, r io.Reader, fileOpts *FileOpts, linkRt LinkRuntime) error {
	err := pc.setRawPathWrap(fullPath, r, fileOpts, linkRt)
	if err != nil {
		pc.logV("Dashborg SetPath ERROR %s => %s | %v\n", dashutil.SimplifyPath(fullPath, nil), shortFileOptsStr(fileOpts), err)
		return err
	}
	pc.logV("Dashborg SetPath %s => %s\n", dashutil.SimplifyPath(fullPath, nil), shortFileOptsStr(fileOpts))
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
	err := dashutil.ValidateFullPath(fullPath, false)
	if err != nil {
		return dasherr.ValidateErr(err)
	}
	if len(fileOpts.AllowedRoles) == 0 {
		fileOpts.AllowedRoles = []string{RoleUser}
	}
	err = fileOpts.Validate()
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
	m := &dashproto.SetPathMessage{
		Ts:             dashutil.Ts(),
		Path:           fullPath,
		HasBody:        (r != nil),
		ConnectRuntime: (linkRt != nil),
		FileOptsJson:   optsJson,
	}
	ctx, cancelFn := pc.ctxWithMd(stdGrpcTimeout)
	defer cancelFn()
	resp, respErr := pc.DBService.SetPath(ctx, m)
	dashErr := pc.handleStatusErrors("SetPath", resp, respErr, false)
	if dashErr != nil {
		return dashErr
	}
	if resp.BlobFound || !m.HasBody {
		if fileOpts.IsLinkType() && linkRt != nil {
			pc.connectLinkRuntime(fullPath, linkRt)
		}
		return nil
	}
	if resp.BlobUploadId == "" || resp.BlobUploadKey == "" {
		return dasherr.NoRetryErrWithCode(dasherr.ErrCodeProtocol, fmt.Errorf("Invalid Server Response, no UploadId/UploadKey specified"))
	}
	if r == nil {
		return dasherr.ValidateErr(fmt.Errorf("Nil Reader passed to SetPath"))
	}
	uploadCtx, uploadCancelFn := context.WithTimeout(context.Background(), uploadTimeout)
	defer uploadCancelFn()
	err = pc.UploadFile(uploadCtx, r, pc.Config.AccId, resp.BlobUploadId, resp.BlobUploadKey)
	if err != nil {
		return err
	}
	return nil
}

func (pc *DashCloudClient) GlobalFSClient() *DashFSClient {
	return &DashFSClient{client: pc}
}

func (pc *DashCloudClient) FSClientAtRoot(rootPath string) (*DashFSClient, error) {
	if rootPath != "" {
		_, _, _, err := dashutil.ParseFullPath(rootPath, false)
		if err != nil {
			return nil, err
		}
		// remove trailing slash
		if rootPath[len(rootPath)-1] == '/' {
			rootPath = rootPath[0 : len(rootPath)-1]
		}
	}
	return &DashFSClient{client: pc, rootPath: rootPath}, nil
}

func (pc *DashCloudClient) AppClient() *DashAppClient {
	return &DashAppClient{pc}
}

func requestMsgStr(reqMsg *dashproto.RequestMessage) string {
	if reqMsg.Path == "" {
		return fmt.Sprintf("[no-path]")
	}
	return fmt.Sprintf("%4s %s", reqMsg.RequestMethod, dashutil.SimplifyPath(reqMsg.Path, nil))
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
		return nil, dasherr.ValidateErr(fmt.Errorf("Invalid Mime-Type passed to SetBlobData mime-type=%s", mimeType))
	}
	first := true
	totalSize := 0
	var rra []*dashproto.RRAction
	for {
		buffer := make([]byte, blobReadSize)
		n, err := io.ReadFull(reader, buffer)
		if err == io.EOF {
			break
		}
		totalSize += n
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
	if totalSize > maxRRABlobSize {
		return nil, dasherr.ValidateErr(fmt.Errorf("BLOB too large, max-size:%d, blob-size:%d", maxRRABlobSize, totalSize))
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

type uploadRespType struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	ErrCode string `json:"errcode"`
	PermErr bool   `json:"permerr"`
}

func (c *Config) getRawUploadUrl() string {
	return fmt.Sprintf("https://%s/api2/raw-upload", c.ConsoleHost)
}

func (pc *DashCloudClient) UploadFile(ctx context.Context, r io.Reader, accId string, uploadId string, uploadKey string) error {
	if !dashutil.IsUUIDValid(accId) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid AccId"))
	}
	if !dashutil.IsUUIDValid(uploadId) || uploadKey == "" {
		return dasherr.ValidateErr(fmt.Errorf("Invalid UploadId/UploadKey"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pc.Config.getRawUploadUrl(), r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Dashborg-AccId", accId)
	req.Header.Set("X-Dashborg-UploadId", uploadId)
	req.Header.Set("X-Dashborg-UploadKey", uploadKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return dasherr.ErrWithCode(dasherr.ErrCodeUpload, err)
	}
	defer resp.Body.Close()
	bodyContent, err := ioutil.ReadAll(resp.Body)
	var uploadResp uploadRespType
	err = json.Unmarshal(bodyContent, &uploadResp)
	if err != nil {
		return dasherr.JsonUnmarshalErr("UploadResponse", err)
	}
	if !uploadResp.Success {
		errMsg := uploadResp.Error
		if errMsg == "" {
			return errors.New("Unknown Error")
		}
		if uploadResp.ErrCode != "" {
			if uploadResp.PermErr {
				return dasherr.NoRetryErrWithCode(dasherr.ErrCode(uploadResp.ErrCode), errors.New(errMsg))
			} else {
				return dasherr.ErrWithCode(dasherr.ErrCode(uploadResp.ErrCode), errors.New(errMsg))
			}
		}
		return errors.New(errMsg)
	}
	return nil
}
