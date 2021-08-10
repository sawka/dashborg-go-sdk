package dashcloud

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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

const grpcServerPath = "/grpc-server"

const (
	mdConnIdKey        = "dashborg-connid"
	mdClientVersionKey = "dashborg-clientversion"
)

const (
	consoleHostProd = "console.dashborg.net"
	consoleHostDev  = "console.dashborg-dev.com:8080"
)

var NotConnectedErr = fmt.Errorf("Dashborg Client is not Connected")

type AppStruct struct {
	AppClient dash.AppClient
	App       dash.AppRuntime
}

type DashCloudClient struct {
	Lock      *sync.Mutex
	StartTime time.Time
	ProcRunId string
	Config    *Config
	Conn      *grpc.ClientConn
	DBService dashproto.DashborgServiceClient
	ConnId    *atomic.Value
	AppMap    map[string]*AppStruct
	DoneCh    chan bool
	PermErr   bool
	ExitErr   error
	AccInfo   *dashproto.AccInfo
}

func makeCloudClient(config *Config) *DashCloudClient {
	rtn := &DashCloudClient{
		Lock:      &sync.Mutex{},
		StartTime: time.Now(),
		ProcRunId: uuid.New().String(),
		Config:    config,
		ConnId:    &atomic.Value{},
		AppMap:    make(map[string]*AppStruct),
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
	err := pc.connectGrpc()
	if err != nil {
		pc.logV("Dashborg ERROR connecting gRPC client: %v\n", err)
	}
	if pc.Config.ShutdownCh != nil {
		go func() {
			<-pc.Config.ShutdownCh
			pc.externalShutdown()
		}()
	}
	if pc.Config.DashborgSrvHost == "" {
		grpcConfig, err := pc.getGrpcServer()
		if err != nil {
			return err
		}
		pc.Config.DashborgSrvHost = grpcConfig.GrpcServer
		pc.Config.DashborgSrvPort = grpcConfig.GrpcPort
		if pc.Config.Verbose {
			log.Printf("Dashborg Using gRPC host %s:%d\n", pc.Config.DashborgSrvHost, pc.Config.DashborgSrvPort)
		}
	}
	if pc.Config.Verbose {
		log.Printf("Dashborg Initialized CloudClient AccId:%s Zone:%s ProcName:%s ProcRunId:%s\n", pc.Config.AccId, pc.Config.ZoneName, pc.Config.ProcName, pc.ProcRunId)
	}
	permErr, err := pc.sendConnectClientMessage(false)
	if permErr {
		pc.setExitError(err)
		return err
	}
	go pc.runRequestStreamLoop()
	return nil
}

func (pc *DashCloudClient) ctxWithMd() context.Context {
	ctx := context.Background()
	connId := pc.ConnId.Load().(string)
	ctx = metadata.AppendToOutgoingContext(ctx, mdConnIdKey, connId, mdClientVersionKey, dash.ClientVersion)

	return ctx
}

func (pc *DashCloudClient) externalShutdown() {
	if pc.Conn == nil {
		pc.logV("Dashborg ERROR shutting down, gRPC connection is not initialized\n")
		return
	}
	pc.setExitError(fmt.Errorf("ShutdownCh channel closed"))
	err := pc.Conn.Close()
	if err != nil {
		pc.logV("Dashborg ERROR closing gRPC connection: %v\n", err)
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

// returns permErr, err
func (pc *DashCloudClient) sendConnectClientMessage(isReconnect bool) (bool, error) {
	// only allow one proc message at a time (synchronize)
	hostData := makeHostData()
	reconApps := make([]string, 0)
	for _, appstruct := range pc.AppMap {
		reconApps = append(reconApps, appstruct.App.GetAppName())
	}
	m := &dashproto.ConnectClientMessage{
		Ts:                   dashutil.Ts(),
		ProcRunId:            pc.ProcRunId,
		AccId:                pc.Config.AccId,
		ZoneName:             pc.Config.ZoneName,
		AnonAcc:              pc.Config.AnonAcc,
		ProcName:             pc.Config.ProcName,
		ProcTags:             pc.Config.ProcTags,
		HostData:             hostData,
		StartTs:              dashutil.DashTime(pc.StartTime),
		ReconnectAppRuntimes: reconApps,
	}
	resp, err := pc.DBService.ConnectClient(pc.ctxWithMd(), m)
	if err != nil {
		log.Printf("Dashborg procclient ConnectClient error: %v\n", err)
		pc.ConnId.Store("")
		return false, err
	}
	if !resp.Success {
		log.Printf("Dashborg procclient ConnectClient error: %s\n", resp.Err)
		pc.Lock.Lock()
		pc.ConnId.Store("")
		pc.PermErr = resp.PermanentError
		pc.Lock.Unlock()
		return resp.PermanentError, errors.New(resp.Err)
	}
	pc.Lock.Lock()
	pc.ConnId.Store(resp.ConnId)
	pc.AccInfo = resp.AccInfo
	pc.Lock.Unlock()
	if !isReconnect {
		if resp.AccInfo.NewAccount {
			pc.printNewAccMessage()
		} else if resp.AccInfo.AccType == "anon" {
			pc.printAnonAccMessage()
		}
		if pc.Config.Verbose {
			log.Printf("DashborgCloudClient Connected, AccId:%s Zone:%s ConnId:%s AccType:%v\n", pc.Config.AccId, pc.Config.ZoneName, resp.ConnId, resp.AccInfo.AccType)
		} else {
			log.Printf("DashborgCloudClient Connected, AccId:%s Zone:%s\n", pc.Config.AccId, pc.Config.ZoneName)
		}
	} else {
		if pc.Config.Verbose {
			log.Printf("DashborgCloudClient ReConnected, AccId:%s Zone:%s ConnId:%s\n", pc.Config.AccId, pc.Config.ZoneName, resp.ConnId)
		}
	}
	return false, nil
}

func (pc *DashCloudClient) printNewAccMessage() {
	log.Printf("Welcome to Dashborg!  Your new account has been provisioned.  AccId:%s\n", pc.Config.AccId)
	log.Printf("You are currently using a free version of the Dashborg Service.\n")
	log.Printf("Your use of this service is subject to the Dashborg Terms of Service - https://www.dashborg.net/static/tos.html\n")
}

func (pc *DashCloudClient) printAnonAccMessage() {
	log.Printf("You are currently using a free version of the Dashborg Service.\n")
	log.Printf("Your use of this service is subject to the Dashborg Terms of Service - https://www.dashborg.net/static/tos.html\n")
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

func (pc *DashCloudClient) showAppLink(appName string) {
	pc.Lock.Lock()
	accInfo := pc.AccInfo
	pc.Lock.Unlock()
	if accInfo == nil {
		return
	}
	if pc.Config.NoShowJWT || !accInfo.AccJWTEnabled {
		log.Printf("DashborgCloudClient App Link [%s]: %s\n", appName, pc.appLink(appName))
	} else {
		appLink, err := pc.MakeJWTAppLink(appName, pc.Config.JWTDuration, pc.Config.JWTUserId, pc.Config.JWTRole)
		if err != nil {
			log.Printf("DashborgCloudClient App Link [%s] Error: %v\n", appName, err)
		} else {
			log.Printf("DashborgCloudClient App Link [%s]: %s\n", appName, appLink)
		}
	}
}

func (pc *DashCloudClient) ConnectApp(app dash.AppRuntime) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	appName := app.GetAppName()
	clientConfig := dash.AppClientConfig{
		Verbose: pc.Config.Verbose,
	}
	appClient := dash.MakeAppClient(pc, app, pc.DBService, clientConfig, pc.ConnId)
	pc.Lock.Lock()
	pc.AppMap[appName] = &AppStruct{App: app, AppClient: appClient}
	pc.Lock.Unlock()
	appConfig := app.GetAppConfig()
	err := pc.baseWriteApp(app.GetAppName(), true, &appConfig, "ConnectApp")
	pc.showAppLink(appName)
	if err != nil {
		log.Printf("DashborgCloudClient error calling ConnectApp app:%s err:%v\n", app.GetAppName(), err)
		return err
	}
	return nil
}

func (pc *DashCloudClient) RemoveApp(appName string) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	m := &dashproto.RemoveAppMessage{
		Ts:      dashutil.Ts(),
		AppName: appName,
	}
	resp, respErr := pc.DBService.RemoveApp(pc.ctxWithMd(), m)
	var err error
	if respErr != nil {
		err = respErr
	} else if resp.Err != "" {
		err = errors.New(resp.Err)
	} else if !resp.Success {
		err = errors.New("Error calling RemoveApp()")
	}
	if err != nil {
		log.Printf("DashborgCloudClient error removing app: %v\n", err)
		return err
	}
	log.Printf("DashborgCloudClient removed app %s\n", appName)
	return nil
}

func (pc *DashCloudClient) ConnectAppRuntime(app dash.AppRuntime) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	appName := app.GetAppName()
	clientConfig := dash.AppClientConfig{
		Verbose: pc.Config.Verbose,
	}
	appClient := dash.MakeAppClient(pc, app, pc.DBService, clientConfig, pc.ConnId)
	pc.Lock.Lock()
	pc.AppMap[appName] = &AppStruct{App: app, AppClient: appClient}
	pc.Lock.Unlock()

	err := pc.baseWriteApp(appName, true, nil, "ConnectAppRuntime")
	pc.showAppLink(appName)
	if err != nil {
		log.Printf("DashborgCloudClient error calling ConnectAppRuntime() app:%s err:%v\n", appName, err)
		return err
	}
	return nil
}

func (pc *DashCloudClient) runRequestStreamLoop() {
	defer close(pc.DoneCh)

	w := &dashutil.ExpoWait{}
	for {
		state := pc.Conn.GetState()
		if state == connectivity.Shutdown {
			log.Printf("Dashborg procclient RunRequestStreamLoop exiting -- Conn Shutdown\n")
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
			permErr, err := pc.sendConnectClientMessage(true)
			if permErr {
				log.Printf("Dashborg procclient RunRequestStreamLoop exiting -- Permanent Error: %v\n", err)
				pc.setExitError(err)
				break
			}
			if err != nil {
				continue
			}
		}
		ranOk, ec := pc.runRequestStream()
		if ranOk {
			w.Reset()
		}
		if ec == dash.ErrBadConnId {
			pc.ConnId.Store("")
			continue
		}
		w.ForceWait = true
	}
}

func (pc *DashCloudClient) sendNoAppResponse(reqMsg *dashproto.RequestMessage) {
	m := &dashproto.SendResponseMessage{
		Ts:           dashutil.Ts(),
		ReqId:        reqMsg.ReqId,
		RequestType:  reqMsg.RequestType,
		PanelName:    reqMsg.PanelName,
		FeClientId:   reqMsg.FeClientId,
		ResponseDone: true,
		Err:          "No App Found",
	}
	_, err := pc.DBService.SendResponse(pc.ctxWithMd(), m)
	if err != nil {
		pc.logV("Error sending No App Response: %v\n", err)
	}
}

func (pc *DashCloudClient) runRequestStream() (bool, string) {
	m := &dashproto.RequestStreamMessage{Ts: dashutil.Ts()}
	pc.logV("Dashborg gRPC RequestStream starting\n")
	reqStreamClient, err := pc.DBService.RequestStream(pc.ctxWithMd(), m)
	if err != nil {
		log.Printf("Dashborg Error setting up gRPC RequestStream: %v\n", err)
		return false, dash.ErrUnknown
	}
	startTime := time.Now()
	var reqCounter int64
	var endingErrCode string
	for {
		reqMsg, err := reqStreamClient.Recv()
		if err == io.EOF {
			pc.logV("Dashborg gRPC RequestStream done: EOF\n")
			endingErrCode = dash.ErrEOF
			break
		}
		if err != nil {
			pc.logV("Dashborg gRPC RequestStream ERROR: %v\n", err)
			endingErrCode = dash.ErrUnknown
			break
		}
		if reqMsg.ErrCode == dashproto.ErrorCode_EC_BADCONNID {
			pc.logV("Dashborg gRPC RequestStream BADCONNID\n")
			endingErrCode = dash.ErrBadConnId
			break
		}
		pc.logV("Dashborg gRPC got request: app=%s, type=%s, path=%s\n", reqMsg.PanelName, reqMsg.RequestType, reqMsg.Path)
		go func() {
			atomic.AddInt64(&reqCounter, 1)
			timeoutMs := reqMsg.TimeoutMs
			if timeoutMs == 0 || timeoutMs > 60000 {
				timeoutMs = 60000
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
			defer cancel()

			appName := reqMsg.PanelName
			pc.Lock.Lock()
			appClient := pc.AppMap[appName]
			pc.Lock.Unlock()
			if appClient == nil {
				pc.sendNoAppResponse(reqMsg)
				return
			}
			appClient.AppClient.DispatchRequest(ctx, reqMsg)
		}()
	}
	elapsed := time.Since(startTime)
	return (elapsed >= 5*time.Second), endingErrCode
}

func (pc *DashCloudClient) logV(fmtStr string, args ...interface{}) {
	if pc.Config.Verbose {
		log.Printf(fmtStr, args...)
	}
}

func (pc *DashCloudClient) BackendPush(panelName string, path string, data interface{}) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	m := &dashproto.BackendPushMessage{
		Ts:        dashutil.Ts(),
		PanelName: panelName,
		Path:      path,
	}
	resp, err := pc.DBService.BackendPush(pc.ctxWithMd(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return errors.New("Error calling BackendPush()")
	}
	return nil
}

func (pc *DashCloudClient) ReflectZone() (*ReflectZoneType, error) {
	if !pc.IsConnected() {
		return nil, NotConnectedErr
	}
	m := &dashproto.ReflectZoneMessage{Ts: dashutil.Ts()}
	resp, err := pc.DBService.ReflectZone(pc.ctxWithMd(), m)
	if err != nil {
		return nil, err
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}
	if !resp.Success {
		return nil, errors.New("Error calling ReflectZone()")
	}
	var rtn ReflectZoneType
	err = json.Unmarshal([]byte(resp.JsonData), &rtn)
	if err != nil {
		return nil, err
	}
	return &rtn, nil
}

func (pc *DashCloudClient) CallDataHandler(panelName string, path string, data interface{}) (interface{}, error) {
	if !pc.IsConnected() {
		return nil, NotConnectedErr
	}
	jsonData, err := dashutil.MarshalJson(data)
	if err != nil {
		return nil, err
	}
	m := &dashproto.CallDataHandlerMessage{
		Ts:        dashutil.Ts(),
		PanelName: panelName,
		Path:      path,
		JsonData:  jsonData,
	}
	resp, err := pc.DBService.CallDataHandler(pc.ctxWithMd(), m)
	if err != nil {
		return nil, err
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}
	if !resp.Success {
		return nil, errors.New("Error calling CallDataHandler()")
	}
	var rtn interface{}
	if resp.JsonData != "" {
		err = json.Unmarshal([]byte(resp.JsonData), &rtn)
		if err != nil {
			return nil, err
		}
	}
	return rtn, nil
}

// Bare streams start with no connected clients.  ControlPath is ignored, and NoServerCancel must be set to true.
// A future request can attach to the stream by calling req.StartStream() and passing the
// same StreamId.  An error will be returned if a stream with this StreamId has already started.
// Unlike StartStream StreamId must be specified ("" will return an error).
// Caller is responsible for calling req.Done() when the stream is finished.
func (pc *DashCloudClient) StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.Request, error) {
	if !pc.IsConnected() {
		return nil, NotConnectedErr
	}
	pc.Lock.Lock()
	app := pc.AppMap[appName]
	pc.Lock.Unlock()
	if app == nil {
		return nil, fmt.Errorf("No active app[%s] found for StartBareStream", appName)
	}
	streamReq, _, err := app.AppClient.StartStream(appName, streamOpts, "")
	return streamReq, err
}

// returns the reason for shutdown (GetExitError())
func (pc *DashCloudClient) WaitForShutdown() error {
	<-pc.DoneCh
	return pc.GetExitError()
}

func (pc *DashCloudClient) OpenApp(appName string) (*dash.App, error) {
	if !pc.IsConnected() {
		return nil, NotConnectedErr
	}
	m := &dashproto.OpenAppMessage{
		Ts:      dashutil.Ts(),
		AppName: appName,
	}
	resp, err := pc.DBService.OpenApp(pc.ctxWithMd(), m)
	if err != nil {
		return nil, err
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}
	if !resp.Success {
		return nil, errors.New("Error calling OpenApp()")
	}
	if resp.AppConfigJson == "" {
		return dash.MakeApp(appName, pc), nil
	}
	var rtn dash.AppConfig
	err = json.Unmarshal([]byte(resp.AppConfigJson), &rtn)
	if err != nil {
		return nil, err
	}
	return dash.MakeAppFromConfig(rtn, pc), nil
}

func (pc *DashCloudClient) baseWriteApp(appName string, shouldConnect bool, acfg *dash.AppConfig, writeAppFnStr string) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	var jsonVal string
	if acfg != nil {
		var err error
		jsonVal, err = dashutil.MarshalJson(acfg)
		if err != nil {
			return err
		}
	}
	m := &dashproto.WriteAppMessage{
		Ts:            dashutil.Ts(),
		AppName:       appName,
		AppConfigJson: jsonVal,
		ConnectApp:    shouldConnect,
	}
	resp, err := pc.DBService.WriteApp(pc.ctxWithMd(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return fmt.Errorf("Error calling %s()", writeAppFnStr)
	}
	for name, warning := range resp.OptionWarnings {
		log.Printf("%s WARNING option[%s]: %s\n", writeAppFnStr, name, warning)
	}
	return nil
}

func (pc *DashCloudClient) WriteApp(acfg dash.AppConfig) error {
	err := pc.baseWriteApp(acfg.AppName, false, &acfg, "WriteApp")
	pc.showAppLink(acfg.AppName)
	if err != nil {
		log.Printf("DashborgCloudClient Error calling WriteApp() app:%s err:%v\n", acfg.AppName, err)
		return err
	}
	return nil
}

func (pc *DashCloudClient) SetBlobData(acfg dash.AppConfig, blob dash.BlobData, r io.Reader) error {
	if !pc.IsConnected() {
		return NotConnectedErr
	}
	blobJson, err := dashutil.MarshalJson(blob)
	if err != nil {
		return err
	}
	barr, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}
	if len(barr) > 5000000 {
		return fmt.Errorf("Max Blob size is 5M")
	}
	m := &dashproto.SetBlobMessage{
		Ts:           dashutil.Ts(),
		AppName:      acfg.AppName,
		AppVersion:   acfg.AppVersion,
		BlobDataJson: blobJson,
		BlobBytes:    barr,
	}
	bclient, err := pc.DBService.SetBlob(pc.ctxWithMd())
	if err != nil {
		return err
	}
	err = bclient.Send(m)
	if err != nil {
		return err
	}
	resp, err := bclient.CloseAndRecv()
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return errors.New("Error calling SetBlob()")
	}
	blob.Size = int64(len(barr))
	return nil
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

func (pc *DashCloudClient) MakeJWTAppLink(appName string, validTime time.Duration, userId string, roleName string) (string, error) {
	if validTime == 0 {
		validTime = 24 * time.Hour
	}
	if roleName == "" {
		roleName = "user"
	}
	if userId == "" {
		userId = "jwt-user"
	}
	jwtToken, err := pc.Config.MakeAccountJWT(validTime, userId, roleName)
	if err != nil {
		return "", err
	}
	link := pc.appLink(appName)
	return fmt.Sprintf("%s?jwt=%s", link, jwtToken), nil
}

func (pc *DashCloudClient) MustMakeJWTAppLink(appName string, validTime time.Duration, userId string, roleName string) string {
	rtn, err := pc.MakeJWTAppLink(appName, validTime, userId, roleName)
	if err != nil {
		panic(err)
	}
	return rtn
}

func (pc *DashCloudClient) getAccHost() string {
	if !pc.IsConnected() {
		panic("DashCloudClient is not connected")
	}
	pc.Lock.Lock()
	defer pc.Lock.Unlock()

	if pc.AccInfo != nil && pc.AccInfo.AccCName != "" {
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

func (pc *DashCloudClient) appLink(appName string) string {
	accHost := pc.getAccHost()
	path := dashutil.MakeAppPath(pc.Config.ZoneName, appName)
	return accHost + path
}
