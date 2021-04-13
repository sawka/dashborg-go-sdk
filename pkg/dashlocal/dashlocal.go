package dashlocal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const LS_VERSION = "go-0.1.0"
const CSRF_COOKIE = "dashcsrf"
const CSRFTOKEN_HEADER = "X-Csrf-Token"
const FECLIENTID_HEADER = "X-Dashborg-FeClientId"

const HTTP_READ_TIMEOUT = 5 * time.Second
const HTTP_WRITE_TIMEOUT = 21 * time.Second
const HTTP_MAX_HEADER_BYTES = 60000
const HTTP_TIMEOUT_VAL = 21 * time.Second

type DashProcClient interface {
	DispatchLocalRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) ([]*dashproto.RRAction, error)
	GetClientVersion() string
	DrainLocalFeStream(ctx context.Context, feClientId string, timeout time.Duration, pushPanel string) ([]*dashproto.RRAction, []string, error)
	StopStream(reqId string) error
}

type errorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

type successResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
}

type Config struct {
	Addr       string        // defaults to localhost:8082
	ShutdownCh chan struct{} // channel for shutting down server
	AccId      string
	ZoneName   string
	PanelName  string
	Env        string
}

type localServer struct {
	Config     *Config
	RootHtml   string
	ProcClient DashProcClient
}

type lsPanelConfig struct {
	AccId         string `json:"accId"`
	ZoneName      string `json:"zoneName"`
	PanelName     string `json:"panelName"`
	ChromeVarName string `json:"chromeVarName,omitempty"`
	Id            string `json:"id"`
	LocalServer   bool   `json:"localServer"`
	LinkToUrl     bool   `json:"linkToUrl"`
	ClientVersion string `json:"clientVersion"`
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

func makeLocalServer(config *Config) *localServer {
	return &localServer{
		Config: config,
	}
}

func (s *localServer) getRootHtmlUrl() string {
	rhRoot := "https://console.dashborg.net/local-server-html"
	if s.Config.Env != "prod" {
		rhRoot = "http://console.dashborg.localdev:8080/local-server-html"
	}
	rhUrl, _ := url.Parse(rhRoot)
	q := rhUrl.Query()
	q.Set("scope", s.Config.AccId+":"+s.Config.ZoneName+":"+s.Config.PanelName)
	q.Set("client", s.ProcClient.GetClientVersion())
	rhUrl.RawQuery = q.Encode()
	return rhUrl.String()
}

func (s *localServer) rootHandler(w http.ResponseWriter, r *http.Request) {
	pconfig := &lsPanelConfig{
		AccId:         s.Config.AccId,
		ZoneName:      s.Config.ZoneName,
		PanelName:     s.Config.PanelName,
		ChromeVarName: "DashborgChromeState",
		Id:            "chromeroot",
		LocalServer:   true,
		ClientVersion: s.ProcClient.GetClientVersion(),
		LinkToUrl:     false,
	}
	w.Header().Set("Cache-Control", "no-cache")
	configJson, err := json.MarshalIndent(pconfig, "", "  ")
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(fmt.Sprintf("Error marshaling config json: %v", err)))
		return
	}
	csrfToken := setCsrfToken(w, r)
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	html := s.RootHtml
	html = strings.Replace(html, "@CSRF-TOKEN", csrfToken, 1)
	html = strings.Replace(html, "@CONFIG", string(configJson), 1)
	if s.Config.Env != "prod" {
		html = strings.Replace(html, "@STATIC-HOST", "http://static.dashborg.localdev:8080", 2)
	} else {
		html = strings.Replace(html, "@STATIC-HOST", "https://static.dashborg.net", 2)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

func (s *localServer) newReq(r *http.Request, rtype string, path string, data interface{}, panelState interface{}) (*dashproto.RequestMessage, error) {
	rtn := &dashproto.RequestMessage{
		Ts:          dashutil.Ts(),
		AccId:       s.Config.AccId,
		ReqId:       uuid.New().String(),
		RequestType: rtype,
		ZoneName:    s.Config.ZoneName,
		PanelName:   s.Config.PanelName,
		Path:        path,
	}
	feClientId := r.Header.Get(FECLIENTID_HEADER)
	if dashutil.IsUUIDValid(feClientId) {
		rtn.FeClientId = feClientId
	}
	if data != nil {
		jsonBytes, err := marshalJson(data)
		if err != nil {
			return nil, err
		}
		rtn.JsonData = string(jsonBytes)
	}
	if panelState != nil {
		jsonBytes, err := marshalJson(panelState)
		if err != nil {
			return nil, err
		}
		rtn.PanelStateData = string(jsonBytes)
	}
	return rtn, nil
}

func decodeParams(r *http.Request, v interface{}) error {
	contentType := r.Header.Get("Content-Type")
	if r.Method == "POST" && strings.HasPrefix(contentType, "application/json") {
		barr, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return err
		}
		err = json.Unmarshal(barr, v)
		return err
	}
	return errors.New("Invalid Method / Content-Type")
}

func jsonWrapper(handler func(w http.ResponseWriter, r *http.Request) (interface{}, error)) func(w http.ResponseWriter, r *http.Request) {
	handlerFn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/json")
		rtn, err := handler(w, r)
		if rtn != nil || (rtn == nil && err == nil) {
			if _, ok := rtn.(successResponse); !ok {
				rtn = successResponse{Success: true, Data: rtn}
			}
		}
		if err != nil {
			rtn = errorResponse{Success: false, Error: err.Error()}
		}
		var jsonRtn string
		jsonRtn, err = marshalJson(rtn)
		if err != nil {
			rtn = errorResponse{Success: false, Error: fmt.Sprintf("Error Marshaling JSON: %v", err)}
			jsonRtn, _ = marshalJson(rtn)
		}
		w.Write([]byte(jsonRtn))
	}
	return handlerFn
}

func setCsrfToken(w http.ResponseWriter, r *http.Request) string {
	csrfToken := ""
	cookie, _ := r.Cookie(CSRF_COOKIE)
	if cookie != nil {
		csrfCookieVal := cookie.Value
		if dashutil.IsUUIDValid(csrfCookieVal) {
			csrfToken = csrfCookieVal
		}
	}
	if csrfToken == "" {
		csrfToken = uuid.New().String()
	}
	cookie = &http.Cookie{
		Name:     CSRF_COOKIE,
		Value:    csrfToken,
		Path:     "/",
		Secure:   false,
		HttpOnly: true,
		MaxAge:   24 * 60 * 60,
	}
	http.SetCookie(w, cookie)
	return csrfToken
}

func checkCsrf(r *http.Request) error {
	cookie, err := r.Cookie(CSRF_COOKIE)
	if err == http.ErrNoCookie || cookie == nil {
		return fmt.Errorf("Bad Request: No CSRF Cookie Found")
	}
	csrfCookieVal := cookie.Value
	if !dashutil.IsUUIDValid(csrfCookieVal) {
		return fmt.Errorf("Bad Request: Malformed CSRF Cookie")
	}
	csrfHeaderVal := r.Header.Get(CSRFTOKEN_HEADER)
	if !dashutil.IsUUIDValid(csrfHeaderVal) {
		return fmt.Errorf("Bad Request: No CSRF Header Set")
	}
	if csrfHeaderVal != csrfCookieVal {
		return fmt.Errorf("Bad Request: CSRF Token Does Not Match")
	}
	return nil
}

func convertRR(rr *dashproto.RRAction, reqId string) map[string]interface{} {
	rtn := make(map[string]interface{})
	rtn["ts"] = rr.Ts
	if reqId != "" {
		rtn["reqid"] = reqId
	}
	rtn["type"] = rr.ActionType
	if rr.Selector != "" {
		rtn["selector"] = rr.Selector
	}
	if rr.EventType != "" {
		rtn["eventtype"] = rr.EventType
	}
	if rr.JsonData != "" {
		var dataI interface{}
		err := json.Unmarshal([]byte(rr.JsonData), &dataI)
		if err != nil {
			rtn["err"] = err.Error()
		} else {
			rtn["data"] = dataI
		}
	}
	if rtn["err"] == nil && rr.Err != "" {
		rtn["err"] = rr.Err
	}
	if rr.Html != "" {
		rtn["html"] = rr.Html
	}
	if len(rr.BlobBytes) > 0 {
		rtn["blobbase64"] = base64.RawStdEncoding.EncodeToString(rr.BlobBytes)
	}
	if rr.BlobMimeType != "" {
		rtn["blobmimetype"] = rr.BlobMimeType
	}
	return rtn
}

func convertRRArr(rrArr []*dashproto.RRAction, reqId string) []interface{} {
	var rtn []interface{}
	for _, rr := range rrArr {
		if rr.ActionType == "panelauth" || rr.ActionType == "panelauthchallenge" {
			continue
		}
		m := convertRR(rr, reqId)
		rtn = append(rtn, m)
	}
	return rtn
}

func (s *localServer) handleLoadPanel(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	type loadPanelParams struct {
		PanelState interface{} `json:"panelstate"`
	}
	var params loadPanelParams
	err := decodeParams(r, &params)
	if err != nil {
		return nil, fmt.Errorf("Cannot decode /api2/load-panel params err:%w", err)
	}
	err = checkCsrf(r)
	if err != nil {
		return nil, err
	}
	rtn := make(map[string]interface{})
	req, err := s.newReq(r, "handler", "/", nil, params.PanelState)
	if err != nil {
		return nil, err
	}
	req.FeClientId = uuid.New().String()
	rra, err := s.ProcClient.DispatchLocalRequest(r.Context(), req)
	if err != nil {
		return nil, err
	}
	rtn["rra"] = convertRRArr(rra, req.ReqId)
	rtn["feclientid"] = req.FeClientId
	return rtn, nil
}

func (s *localServer) handleData(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	type dataParams struct {
		Path       string      `json:"path"`
		Data       interface{} `json:"data"`
		PanelState interface{} `json:"panelstate"`
	}
	var params dataParams
	err := decodeParams(r, &params)
	if err != nil {
		return nil, fmt.Errorf("Cannot decode /api2/data params err:%w", err)
	}
	if !dashutil.IsPathValid(params.Path) {
		return nil, fmt.Errorf("Invalid path")
	}
	err = checkCsrf(r)
	if err != nil {
		return nil, err
	}
	req, err := s.newReq(r, "data", params.Path, params.Data, params.PanelState)
	if err != nil {
		return nil, err
	}
	// FeClientId
	rra, err := s.ProcClient.DispatchLocalRequest(r.Context(), req)
	if err != nil {
		return nil, err
	}
	var rtnJson string
	for _, rr := range rra {
		if rr.ActionType == "setdata" && rr.Selector == "" {
			rtnJson = rr.JsonData
		} else if rr.ActionType == "error" {
			return nil, errors.New(rr.Err)
		} else {
			log.Printf("Bad rr returned from data handler -- type:%s | sel:%s\n", rr.ActionType, rr.Selector)
		}
	}
	var rtn interface{}
	err = json.Unmarshal([]byte(rtnJson), &rtn)
	if err != nil {
		return nil, err
	}
	return rtn, nil
}

func (s *localServer) handleCallHandler(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	type callHandlerParams struct {
		Handler    string      `json:"handler"`
		Data       interface{} `json:"data"`
		PanelState interface{} `json:"panelstate"`
	}
	var params callHandlerParams
	err := decodeParams(r, &params)
	if err != nil {
		return nil, fmt.Errorf("Cannot decode /api2/call-handler params err:%w", err)
	}
	if !dashutil.IsPathValid(params.Handler) {
		return nil, fmt.Errorf("Invalid Handler String")
	}
	err = checkCsrf(r)
	if err != nil {
		return nil, err
	}
	req, err := s.newReq(r, "handler", params.Handler, params.Data, params.PanelState)
	if err != nil {
		return nil, err
	}
	// FeClientId
	rra, err := s.ProcClient.DispatchLocalRequest(r.Context(), req)
	if err != nil {
		return nil, err
	}
	for _, rr := range rra {
		if rr.ActionType == "error" {
			return nil, errors.New(rr.Err)
		}
	}
	rtn := make(map[string]interface{})
	rtn["rra"] = convertRRArr(rra, req.ReqId)
	rtn["reqid"] = req.ReqId
	return rtn, nil
}

func (s *localServer) handleDrainStreams(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	type drainStreamsParams struct {
		AllowPush bool `json:"allowpush"`
	}
	var params drainStreamsParams
	err := decodeParams(r, &params)
	if err != nil {
		return nil, fmt.Errorf("Cannot decode /api2/drain-streams params err:%w", err)
	}
	err = checkCsrf(r)
	if err != nil {
		return nil, err
	}
	feClientId := r.Header.Get(FECLIENTID_HEADER)
	if !dashutil.IsUUIDValid(feClientId) {
		return nil, fmt.Errorf("No FeClientId")
	}
	pushPanel := ""
	if params.AllowPush {
		pushPanel = s.Config.PanelName
	}
	rra, reqIds, err := s.ProcClient.DrainLocalFeStream(r.Context(), feClientId, 10*time.Second, pushPanel)
	rtn := make(map[string]interface{})
	rtn["reqids"] = reqIds
	if err == dashutil.TimeoutErr {
		rtn["timeout"] = true
		return rtn, nil
	}
	if err == dashutil.NoFeStreamErr {
		rtn["nostream"] = true
		return rtn, nil
	}
	if err != nil {
		return nil, err
	}
	rtn["rra"] = convertRRArr(rra, "")
	return rtn, nil
}

func (s *localServer) handleStopStream(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	type stopStreamParams struct {
		ReqId string `json:"reqid"`
	}
	var params stopStreamParams
	err := decodeParams(r, &params)
	if err != nil {
		return nil, fmt.Errorf("Cannot decode /api2/stop-stream params err:%w", err)
	}
	err = checkCsrf(r)
	if err != nil {
		return nil, err
	}
	err = s.ProcClient.StopStream(params.ReqId)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *localServer) registerLocalHandlers() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/", s.rootHandler)
	m.HandleFunc("/api2/load-panel", jsonWrapper(s.handleLoadPanel))
	m.HandleFunc("/api2/data", jsonWrapper(s.handleData))
	m.HandleFunc("/api2/call-handler", jsonWrapper(s.handleCallHandler))
	m.HandleFunc("/api2/drain-streams", jsonWrapper(s.handleDrainStreams))
	m.HandleFunc("/api2/stop-stream", jsonWrapper(s.handleStopStream))
	return m
}

func (s *localServer) getLocalHtml() error {
	rhUrl := s.getRootHtmlUrl()
	req, err := http.NewRequest("GET", rhUrl, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Unwrap(err) != nil {
			return errors.Unwrap(err)
		}
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Non 200 Status from retrieving Dashborg local.html")
	}
	barr, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	s.RootHtml = string(barr)
	return nil
}

func StartLocalServer(config *Config, pc DashProcClient) error {
	if pc == nil {
		panic("DashProcClient cannot be nil")
	}
	if config == nil {
		panic("Config cannot be nil")
	}
	if config.Addr == "" {
		return fmt.Errorf("Addr not set in Config")
	}
	if !dashutil.IsUUIDValid(config.AccId) || !dashutil.IsZoneNameValid(config.ZoneName) || !dashutil.IsPanelNameValid(config.PanelName) {
		return fmt.Errorf("Invalid Configuration AccId/ZoneName/PanelName")
	}
	s := makeLocalServer(config)
	s.ProcClient = pc
	err := s.getLocalHtml()
	if err != nil {
		return fmt.Errorf("Cannot contact Dashborg Service to download HTML chrome for Local Server: %w", err)
	}
	smux := s.registerLocalHandlers()
	httpServer := &http.Server{
		Addr:           s.Config.Addr,
		ReadTimeout:    HTTP_READ_TIMEOUT,
		WriteTimeout:   HTTP_WRITE_TIMEOUT,
		MaxHeaderBytes: HTTP_MAX_HEADER_BYTES,
		Handler:        http.TimeoutHandler(smux, HTTP_TIMEOUT_VAL, "Timeout"),
	}
	if s.Config.ShutdownCh != nil {
		go func() {
			<-config.ShutdownCh
			httpServer.Shutdown(context.Background())
		}()
	}
	log.Printf("Dashborg Local Server for panel[%s] starting at http://%s\n", s.Config.PanelName, s.Config.Addr)
	err = httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Printf("Dashborg Local Server error:%v\n", err)
		return err
	}
	log.Printf("Dashborg Local Server Shutdown\n", err)
	return nil
}
