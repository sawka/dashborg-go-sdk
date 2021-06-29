package dashcloud

import (
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
}

func makeCloudClient(config *Config) *dashCloudClient {
	rtn := &dashCloudClient{
		Lock:      &sync.Mutex{},
		StartTime: time.Now(),
		ProcRunId: uuid.New().String(),
		Config:    config,
		ConnId:    &atomic.Value{},
		AppMap:    make(map[string]*AppStruct),
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
	log.Printf("Dashborg Initialized Client AccId:%s Zone:%s ProcName:%s ProcRunId:%s\n", pc.Config.AccId, pc.Config.ZoneName, pc.Config.ProcName, pc.ProcRunId)
	pc.sendProcMessage()
	go pc.runRequestStreamLoop()
}

func (pc *dashCloudClient) ctxWithMd() context.Context {
	ctx := context.Background()
	connId := pc.ConnId.Load().(string)
	ctx = metadata.AppendToOutgoingContext(ctx, "dashborg-connid", connId)
	return ctx
}

func (pc *dashCloudClient) shutdown() {
	if pc.Conn == nil {
		return
	}
	err := pc.Conn.Close()
	if err != nil {
		logV("Dashborg ERROR closing gRPC connection: %v\n", err)
	}
}

func (pc *dashCloudClient) sendProcMessage() error {
	// only allow one proc message at a time (synchronize)
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	hostData := map[string]string{
		"HostName": hostname,
		"Pid":      strconv.Itoa(os.Getpid()),
	}
	apps := pc.makeAppMessages()
	if err != nil {
		return err
	}
	m := &dashproto.ProcMessage{
		Ts:            dashutil.Ts(),
		ProcRunId:     pc.ProcRunId,
		AccId:         pc.Config.AccId,
		ZoneName:      pc.Config.ZoneName,
		AnonAcc:       pc.Config.AnonAcc,
		ProcName:      pc.Config.ProcName,
		ProcTags:      pc.Config.ProcTags,
		HostData:      hostData,
		StartTs:       dashutil.DashTime(pc.StartTime),
		Apps:          apps,
		ClientVersion: dash.ClientVersion,
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

func (pc *dashCloudClient) makeAppMessages() []*dashproto.ConnectAppMessage {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()

	rtn := make([]*dashproto.ConnectAppMessage, 0, len(pc.AppMap))
	for _, app := range pc.AppMap {
		m, err := makeAppMessage(app.App)
		if err != nil {
			log.Printf("Dashborg ERROR serializing AppConfig: %v\n", err)
			continue
		}
		rtn = append(rtn, m)
	}
	return rtn
}

func appLink(appName string) string {
	accId := globalClient.Config.AccId
	zoneName := globalClient.Config.ZoneName
	if globalClient.Config.Env != "prod" {
		return fmt.Sprintf("https://acc-%s.console.dashborg-dev.com:8080/zone/%s/%s", accId, zoneName, appName)
	}
	return fmt.Sprintf("https://acc-%s.console.dashborg.net/zone/%s/%s", accId, zoneName, appName)
}

func (pc *dashCloudClient) ConnectApp(app dash.AppRuntime) error {
	err := pc.connectAppInternal(app)
	if err != nil {
		log.Printf("Dashborg CloudContainer, error connecting app: %v\n", err)
		return err
	}
	appName := app.GetAppName()
	log.Printf("Dashborg CloudContainer App Link [%s]: %s\n", appName, appLink(appName))
	return nil
}

func (pc *dashCloudClient) connectAppInternal(app dash.AppRuntime) error {
	appName := app.GetAppName()
	m, err := makeAppMessage(app)
	if err != nil {
		return err
	}
	resp, err := globalClient.DBService.ConnectApp(globalClient.ctxWithMd(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return errors.New("Error calling ConnectApp()")
	}
	for name, warning := range resp.OptionWarnings {
		log.Printf("ConnectApp WARNING option[%s]: %s\n", name, warning)
	}
	certInfo, err := readCertInfo(pc.Config.CertFileName)
	if err != nil {
		// strange since client is already running
		return fmt.Errorf("Error reading cert info: %w", err)
	}
	clientConfig := dash.AppClientConfig{
		PublicKey: certInfo.PublicKey,
		Verbose:   pc.Config.Verbose,
	}
	appClient := dash.MakeAppClient(pc, app, pc.DBService, clientConfig, pc.ConnId)
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	pc.AppMap[appName] = &AppStruct{App: app, AppClient: appClient}
	return nil
}

func makeAppMessage(app dash.AppRuntime) (*dashproto.ConnectAppMessage, error) {
	appConfig := app.AppConfig()
	m := &dashproto.ConnectAppMessage{Ts: dashutil.Ts()}
	m.AppName = appConfig.AppName
	m.AppType = appConfig.AppType
	m.Options = make(map[string]string)
	for name, val := range appConfig.Options {
		jsonVal, err := dashutil.MarshalJson(val)
		if err != nil {
			return nil, err
		}
		m.Options[name] = jsonVal
	}
	return m, nil
}

func (pc *dashCloudClient) runRequestStreamLoop() {
	if pc.Config.LocalServer {
		pc.runRequestStream()
		return
	}

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
