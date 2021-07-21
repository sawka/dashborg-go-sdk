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

var globalClient *dashCloudClient

type AppStruct struct {
	AppClient dash.AppClient
	App       dash.AppRuntime
}

type dashCloudClient struct {
	Lock      *sync.Mutex
	StartTime time.Time
	ProcRunId string
	Config    *Config
	Conn      *grpc.ClientConn
	DBService dashproto.DashborgServiceClient
	ConnId    *atomic.Value
	AppMap    map[string]*AppStruct
	DoneCh    chan bool
}

func makeCloudClient(config *Config) *dashCloudClient {
	rtn := &dashCloudClient{
		Lock:      &sync.Mutex{},
		StartTime: time.Now(),
		ProcRunId: uuid.New().String(),
		Config:    config,
		ConnId:    &atomic.Value{},
		AppMap:    make(map[string]*AppStruct),
		DoneCh:    make(chan bool),
	}
	rtn.ConnId.Store("")
	globalClient = rtn
	return rtn
}

func (pc *dashCloudClient) startClient() {
	err := pc.connectGrpc()
	if err != nil {
		logV("Dashborg ERROR connecting gRPC client: %v\n", err)
	}
	if pc.Config.ShutdownCh != nil {
		go func() {
			<-pc.Config.ShutdownCh
			pc.shutdown()
		}()
	}
	log.Printf("Dashborg Initialized CloudClient AccId:%s Zone:%s ProcName:%s ProcRunId:%s\n", pc.Config.AccId, pc.Config.ZoneName, pc.Config.ProcName, pc.ProcRunId)
	pc.sendProcMessage()
	go pc.runRequestStreamLoop()
}

func (pc *dashCloudClient) appCtx() context.Context {
	return context.Background()
}

func (pc *dashCloudClient) ctxWithMd() context.Context {
	ctx := context.Background()
	connId := pc.ConnId.Load().(string)
	ctx = metadata.AppendToOutgoingContext(ctx, "dashborg-connid", connId)
	return ctx
}

func (pc *dashCloudClient) shutdown() {
	if pc.Conn == nil {
		logV("Dashborg ERROR shutting down, gRPC connection is not initialized\n")
		return
	}
	err := pc.Conn.Close()
	if err != nil {
		logV("Dashborg ERROR closing gRPC connection: %v\n", err)
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

func (pc *dashCloudClient) sendProcMessage() error {
	// only allow one proc message at a time (synchronize)
	hostData := makeHostData()
	reconApps := make([]string, 0)
	for _, appstruct := range pc.AppMap {
		reconApps = append(reconApps, appstruct.App.GetAppConfig().AppName)
	}
	m := &dashproto.ProcMessage{
		Ts:                   dashutil.Ts(),
		ProcRunId:            pc.ProcRunId,
		AccId:                pc.Config.AccId,
		ZoneName:             pc.Config.ZoneName,
		AnonAcc:              pc.Config.AnonAcc,
		ProcName:             pc.Config.ProcName,
		ProcTags:             pc.Config.ProcTags,
		HostData:             hostData,
		StartTs:              dashutil.DashTime(pc.StartTime),
		ClientVersion:        dash.ClientVersion,
		ReconnectAppRuntimes: reconApps,
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
		log.Printf("procclient sendProcMessage success connid:%s acctype:%s\n", resp.ConnId, resp.AccType)
	}
	return nil
}

func (pc *dashCloudClient) connectGrpc() error {
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

func (pc *dashCloudClient) showAppLink(appName string) {
	if pc.Config.NoShowJWT {
		log.Printf("Dashborg CloudContainer App Link [%s]: %s\n", appName, pc.Config.appLink(appName))
	} else {
		appLink, err := pc.Config.MakeJWTAppLink(appName, pc.Config.JWTDuration, pc.Config.JWTUserId, pc.Config.JWTRole)
		if err != nil {
			log.Printf("Dashborg CloudContainer App Link [%s] Error: %v\n", appName, err)
		} else {
			log.Printf("Dashborg CloudContainer App Link [%s]: %s\n", appName, appLink)
		}
	}
}

func (pc *dashCloudClient) ConnectApp(app dash.AppRuntime) error {
	jsonConfig, err := dashutil.MarshalJson(app.GetAppConfig())
	if err != nil {
		return err
	}
	appName := app.GetAppConfig().AppName
	clientConfig := dash.AppClientConfig{
		Verbose: pc.Config.Verbose,
	}
	appClient := dash.MakeAppClient(pc, app, pc.DBService, clientConfig, pc.ConnId)
	pc.Lock.Lock()
	pc.AppMap[appName] = &AppStruct{App: app, AppClient: appClient}
	pc.Lock.Unlock()
	m := &dashproto.WriteAppMessage{
		Ts:            dashutil.Ts(),
		ZoneName:      pc.Config.ZoneName,
		AppName:       appName,
		AppConfigJson: jsonConfig,
		ConnectApp:    true,
	}
	resp, grpcErr := globalClient.DBService.WriteApp(globalClient.ctxWithMd(), m)
	if grpcErr != nil {
		err = grpcErr
	} else if resp.Err != "" {
		err = errors.New(resp.Err)
	} else if !resp.Success {
		err = errors.New("Error calling ConnectApp()")
	}
	if err == nil {
		for name, warning := range resp.OptionWarnings {
			log.Printf("ConnectApp WARNING option[%s]: %s\n", name, warning)
		}
	}
	pc.showAppLink(appName)
	if err != nil {
		log.Printf("Dashborg CloudContainer, error connecting app: %v\n", err)
		return err
	}
	return nil
}

func (pc *dashCloudClient) ConnectAppRuntime(app dash.AppRuntime) error {
	appName := app.GetAppConfig().AppName
	clientConfig := dash.AppClientConfig{
		Verbose: pc.Config.Verbose,
	}
	appClient := dash.MakeAppClient(pc, app, pc.DBService, clientConfig, pc.ConnId)
	pc.Lock.Lock()
	pc.AppMap[appName] = &AppStruct{App: app, AppClient: appClient}
	pc.Lock.Unlock()
	m := &dashproto.WriteAppMessage{
		Ts:         dashutil.Ts(),
		ZoneName:   pc.Config.ZoneName,
		AppName:    appName,
		ConnectApp: true,
	}
	resp, err := globalClient.DBService.WriteApp(globalClient.ctxWithMd(), m)
	if err != nil {
		return err
	} else if resp.Err != "" {
		err = errors.New(resp.Err)
	} else if !resp.Success {
		err = errors.New("Error calling ConnectAppRuntime()")
	}
	pc.showAppLink(appName)
	if err != nil {
		log.Printf("Dashborg CloudContainer, error connecting app: %v\n", err)
		return err
	}
	return nil
}

func (pc *dashCloudClient) runRequestStreamLoop() {
	defer close(pc.DoneCh)

	w := &dashutil.ExpoWait{}
	for {
		state := pc.Conn.GetState()
		if state == connectivity.Shutdown {
			log.Printf("Dashborg procclient RunRequestStreamLoop exiting -- Conn Shutdown\n")
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
		if ec == dash.ErrBadConnId {
			pc.ConnId.Store("")
			continue
		}
		w.ForceWait = true
	}
}

func (pc *dashCloudClient) sendNoAppResponse(reqMsg *dashproto.RequestMessage) {
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
		logV("Error sending No App Response: %v\n", err)
	}
}

func (pc *dashCloudClient) runRequestStream() (bool, string) {
	m := &dashproto.RequestStreamMessage{Ts: dashutil.Ts()}
	logV("Dashborg gRPC RequestStream starting\n")
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
			logV("Dashborg gRPC RequestStream done: EOF\n")
			endingErrCode = dash.ErrEOF
			break
		}
		if err != nil {
			logV("Dashborg gRPC RequestStream ERROR: %v\n", err)
			endingErrCode = dash.ErrUnknown
			break
		}
		if reqMsg.ErrCode == dashproto.ErrorCode_EC_BADCONNID {
			logV("Dashborg gRPC RequestStream BADCONNID\n")
			endingErrCode = dash.ErrBadConnId
			break
		}
		logV("Dashborg gRPC got request: app=%s, type=%s, path=%s\n", reqMsg.PanelName, reqMsg.RequestType, reqMsg.Path)
		go func() {
			atomic.AddInt64(&reqCounter, 1)
			timeoutMs := reqMsg.TimeoutMs
			if timeoutMs == 0 || timeoutMs > 60000 {
				timeoutMs = 60000
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(reqMsg.TimeoutMs)*time.Millisecond)
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

func logV(fmtStr string, args ...interface{}) {
	if globalClient != nil && globalClient.Config.Verbose {
		log.Printf(fmtStr, args...)
	}
}

func (pc *dashCloudClient) BackendPush(panelName string, path string, data interface{}) error {
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

func (pc *dashCloudClient) ReflectZone() (*ReflectZoneType, error) {
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

func (pc *dashCloudClient) CallDataHandler(panelName string, path string, data interface{}) (interface{}, error) {
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
func (pc *dashCloudClient) StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.Request, error) {
	pc.Lock.Lock()
	app := pc.AppMap[appName]
	pc.Lock.Unlock()
	if app == nil {
		return nil, fmt.Errorf("No active app[%s] found for StartBareStream", appName)
	}
	streamReq, _, err := app.AppClient.StartStream(appName, streamOpts, "")
	return streamReq, err
}

func (pc *dashCloudClient) WaitForShutdown() error {
	<-pc.DoneCh
	return nil
}

func (pc *dashCloudClient) OpenApp(appName string) (*dash.App, error) {
	m := &dashproto.OpenAppMessage{
		Ts:       dashutil.Ts(),
		ZoneName: pc.Config.ZoneName,
		AppName:  appName,
	}
	resp, err := pc.DBService.OpenApp(pc.appCtx(), m)
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
		// no app found, return new app
		return dash.MakeApp(appName), nil
	}
	var rtn dash.AppConfig
	err = json.Unmarshal([]byte(resp.AppConfigJson), &rtn)
	if err != nil {
		return nil, err
	}
	return dash.MakeAppFromConfig(rtn), nil
}

func (pc *dashCloudClient) WriteApp(acfg dash.AppConfig) error {
	jsonVal, err := dashutil.MarshalJson(acfg)
	if err != nil {
		return err
	}
	m := &dashproto.WriteAppMessage{
		Ts:            dashutil.Ts(),
		ZoneName:      pc.Config.ZoneName,
		AppConfigJson: jsonVal,
	}
	resp, err := pc.DBService.WriteApp(pc.ctxWithMd(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return errors.New("Error calling WriteApp()")
	}
	for name, warning := range resp.OptionWarnings {
		log.Printf("WriteApp WARNING option[%s]: %s\n", name, warning)
	}
	return nil
}

func (pc *dashCloudClient) SetBlob(acfg dash.AppConfig, blob *dash.BlobData, r io.Reader) error {
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
		ZoneName:     pc.Config.ZoneName,
		AppName:      acfg.AppName,
		AppVersion:   acfg.AppVersion,
		BlobDataJson: blobJson,
		BlobBytes:    barr,
	}
	bclient, err := pc.DBService.SetBlob(pc.appCtx())
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

type appBlobManager struct {
	App    *dash.App
	Client *dashCloudClient
}

func (pc *dashCloudClient) AppBlobManager(app *dash.App) dash.BlobManager {
	return appBlobManager{App: app, Client: pc}
}

func (bm appBlobManager) SetBlobData(key string, mimeType string, reader io.Reader, metadata interface{}) error {
	blob := &dash.BlobData{
		BlobKey:  key,
		MimeType: mimeType,
		UpdateTs: dashutil.Ts(),
		Metadata: metadata,
	}
	err := bm.Client.SetBlob(bm.App.AppConfig, blob, reader)
	return err
}

func (bm appBlobManager) SetBlobDataFromFile(key string, mimeType string, fileName string, metadata interface{}) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	return bm.SetBlobData(key, mimeType, fd, metadata)
}
