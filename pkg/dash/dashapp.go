package dash

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

var notAuthorizedErr = fmt.Errorf("Not Authorized")

const MaxAppConfigSize = 1000000
const htmlMimeType = "text/html"
const appBlobNs = "app"
const htmlBlobNs = "html"
const rootHtmlKey = htmlBlobNs + ":" + "root"
const jsonMimeType = "application/json"

const (
	requestTypeHtml    = "html"
	requestTypeInit    = "init"
	requestTypeData    = "data"
	requestTypeAuth    = "auth"
	requestTypeStream  = "stream"
	requestTypeHandler = "handler"
)

const (
	handlerPathInit = "/@init"
	handlerPathHtml = "/@html"
)

const (
	OptionInitHandler   = "inithandler"
	OptionHtml          = "html"
	OptionAuth          = "auth"
	OptionOfflineMode   = "offlinemode"
	OptionTitle         = "title"
	OptionAppVisibility = "visibility"

	AuthTypeZone = "zone"

	VisTypeHidden        = "hidden"  // always hide
	VisTypeDefault       = "default" // shown if user has permission
	VisTypeAlwaysVisible = "visible" // always show

	HtmlTypeStatic               = "static"
	HtmlTypeDynamicWhenConnected = "dynamic-when-connected"
	HtmlTypeDynamic              = "dynamic"

	InitHandlerRequired              = "required"
	InitHandlerRequiredWhenConnected = "required-when-connected"
	InitHandlerNone                  = "none"

	OfflineModeEnable  = "enable"
	OfflineModeDisable = "disable"
)

// AppConfig is passed as JSON to the container.  this struct
// helps with marshaling/unmarshaling the structure.
type AppConfig struct {
	AppName            string                      `json:"appname"`
	AppVersion         string                      `json:"appversion,omitempty"` // uuid
	UpdatedTs          int64                       `json:"updatedts"`            // set by container
	ProcRunId          string                      `json:"procrunid"`            // set by container
	ClientVersion      string                      `json:"clientversion"`
	Options            map[string]GenericAppOption `json:"options"`
	ClearExistingBlobs bool                        `json:"clearexistingblobs,omitempty"`
}

type AppId struct {
	AppName    string
	AppVersion string
}

// super-set of all option fields for JSON marshaling/parsing
type GenericAppOption struct {
	Type         string   `json:"type,omitempty"`
	Path         string   `json:"path,omitempty"`
	AllowedRoles []string `json:"allowedroles,omitempty"`
	Enabled      bool     `json:"enabled,omitempty"`
	AppTitle     string   `json:"apptitle,omitempty"`
	Order        float64  `json:"order,omitempty"`
}

type BlobData struct {
	BlobNs   string      `json:"blobns"`
	BlobKey  string      `json:"blobkey"`
	MimeType string      `json:"mimetype"`
	Size     int64       `json:"size"`
	Sha256   string      `json:"sha256"`
	UpdateTs int64       `json:"updatets"`
	Metadata interface{} `json:"metadata"`
	Removed  bool        `json:"removed"`
}

type middlewareType struct {
	Name     string
	Fn       MiddlewareFuncType
	Priority float64
}

type MiddlewareNextFuncType func(req *Request) (interface{}, error)
type MiddlewareFuncType func(req *Request, nextFn MiddlewareNextFuncType) (interface{}, error)

func (b BlobData) ExtBlobKey() string {
	return fmt.Sprintf("%s:%s", b.BlobNs, b.BlobKey)
}

type ProcInfo struct {
	StartTs   int64
	ProcRunId string
	ProcName  string
	ProcTags  map[string]string
	HostData  map[string]string
}

type AppRuntime interface {
	GetAppName() string
	RunHandler(req *Request) (interface{}, error)
}

type AppRuntimeImpl struct {
	appName      string
	lock         *sync.Mutex
	appStateType reflect.Type
	html         valueType
	handlers     map[string]handlerType
	middlewares  []middlewareType
}

type App struct {
	AppConfig  AppConfig
	appRuntime *AppRuntimeImpl
	api        InternalApi
	isNewApp   bool

	// liveUpdateMode  bool
	// connectOnlyMode bool
}

func (app *App) Runtime() *AppRuntimeImpl {
	return app.appRuntime
}

type valueType interface {
	IsDynamic() bool
	GetValue() (interface{}, error)
}

type funcValueType struct {
	Dyn     bool
	ValueFn func() (string, error)
}

func fileValue(fileName string, isDynamic bool) valueType {
	return funcValueType{
		Dyn: isDynamic,
		ValueFn: func() (string, error) {
			fd, err := os.Open(fileName)
			if err != nil {
				return "", err
			}
			fileBytes, err := ioutil.ReadAll(fd)
			if err != nil {
				return "", err
			}
			return string(fileBytes), nil
		},
	}
}

func stringValue(val string) valueType {
	return funcValueType{
		Dyn: false,
		ValueFn: func() (string, error) {
			return val, nil
		},
	}
}

func interfaceValue(val interface{}) valueType {
	return funcValueType{
		Dyn: false,
		ValueFn: func() (string, error) {
			return dashutil.MarshalJson(val)
		},
	}
}

func funcValue(fn func() (interface{}, error), isDynamic bool) valueType {
	return funcValueType{
		Dyn: isDynamic,
		ValueFn: func() (string, error) {
			val, err := fn()
			if err != nil {
				return "", err
			}
			return dashutil.MarshalJson(val)
		},
	}
}

func (fv funcValueType) IsDynamic() bool {
	return fv.Dyn
}

func (fv funcValueType) GetValue() (interface{}, error) {
	return fv.ValueFn()
}

func defaultAuthOpt() GenericAppOption {
	authOpt := GenericAppOption{
		Type:         AuthTypeZone,
		AllowedRoles: []string{"user"},
	}
	return authOpt
}

func makeAppRuntime(appName string) *AppRuntimeImpl {
	rtn := &AppRuntimeImpl{
		appName: appName,
		lock:    &sync.Mutex{},
	}
	rtn.handlers = make(map[string]handlerType)
	rtn.handlers[handlerPathHtml] = handlerType{HandlerFn: rtn.htmlHandler}
	return rtn
}

func MakeApp(appName string, api InternalApi) *App {
	rtn := &App{
		api:        api,
		appRuntime: makeAppRuntime(appName),
		AppConfig: AppConfig{
			AppVersion: uuid.New().String(),
			AppName:    appName,
		},
		isNewApp: true,
	}
	rtn.AppConfig.Options = make(map[string]GenericAppOption)
	authOpt := defaultAuthOpt()
	rtn.AppConfig.Options[OptionAuth] = authOpt
	rtn.AppConfig.Options[OptionOfflineMode] = GenericAppOption{Type: "allow"}
	return rtn
}

// offline mode type is either OfflineModeEnable or OfflineModeDisable
func (app *App) SetOfflineModeType(offlineModeType string) {
	app.AppConfig.Options[OptionOfflineMode] = GenericAppOption{Type: offlineModeType}
}

func (app *App) IsNew() bool {
	return app.isNewApp
}

// func (app *App) SetConnectOnly(connectOnly bool) {
// 	app.connectOnlyMode = connectOnly
// }

// func (app *App) SetLiveUpdate(liveUpdate bool) {
// 	app.liveUpdateMode = liveUpdate
// }

func (app *App) ClearExistingBlobs() {
	app.AppConfig.ClearExistingBlobs = true
}

func MakeAppFromConfig(cfg AppConfig, api InternalApi) *App {
	rtn := &App{
		api:        api,
		appRuntime: makeAppRuntime(cfg.AppName),
		AppConfig:  cfg,
	}
	rtn.AppConfig.AppVersion = uuid.New().String()
	return rtn
}

type handlerType struct {
	HandlerFn func(req *Request) (interface{}, error)
}

func (app *App) GetAppConfig() AppConfig {
	return app.AppConfig
}

func (app *AppRuntimeImpl) setHandler(path string, handler handlerType) {
	app.lock.Lock()
	defer app.lock.Unlock()
	app.handlers[path] = handler
}

func (apprt *AppRuntimeImpl) RunHandler(req *Request) (interface{}, error) {
	path := req.info.Path
	apprt.lock.Lock()
	hval, ok := apprt.handlers[path]
	mws := apprt.middlewares
	apprt.lock.Unlock()
	if !ok {
		return nil, fmt.Errorf("No handler found for %s:%s", req.info.AppName, req.info.Path)
	}
	rtn, err := mwHelper(req, hval, mws, 0)
	if err != nil {
		return nil, err
	}
	return rtn, nil
}

func mwHelper(outerReq *Request, hval handlerType, mws []middlewareType, mwPos int) (interface{}, error) {
	if mwPos >= len(mws) {
		return hval.HandlerFn(outerReq)
	}
	mw := mws[mwPos]
	return mw.Fn(outerReq, func(innerReq *Request) (interface{}, error) {
		if innerReq == nil {
			panic("No Request Passed to middleware nextFn")
		}
		return mwHelper(innerReq, hval, mws, mwPos+1)
	})
}

func (app *App) RemoveOption(optName string) {
	delete(app.AppConfig.Options, optName)
}

func (app *App) SetOption(optName string, opt GenericAppOption) {
	app.AppConfig.Options[optName] = opt
}

func wrapHandler(handlerFn func(req *Request) error) func(req *Request) (interface{}, error) {
	wrappedHandlerFn := func(req *Request) (interface{}, error) {
		err := handlerFn(req)
		return nil, err
	}
	return wrappedHandlerFn
}

func (app *App) getAuthOpt() GenericAppOption {
	authOpt, ok := app.AppConfig.Options[OptionAuth]
	if !ok {
		return defaultAuthOpt()
	}
	return authOpt
}

// authType must be AuthTypeZone
func (app *App) SetAuthType(authType string) {
	authOpt := app.getAuthOpt()
	authOpt.Type = authType
	app.AppConfig.Options[OptionAuth] = authOpt
}

func (app *App) SetAllowedRoles(roles ...string) {
	authOpt := app.getAuthOpt()
	authOpt.AllowedRoles = roles
	app.AppConfig.Options[OptionAuth] = authOpt
}

// SetAppVisibility controls whether the app shows in the UI's app-switcher (see VisType constants)
// Apps will be sorted by displayOrder (and then AppTitle).  displayOrder of 0 (the default) will
// sort to the end of the list, not the beginning
// visType is either VisTypeHidden, VisTypeDefault, or VisTypeAlwaysVisible
func (app *App) SetAppVisibility(visType string, displayOrder float64) {
	visOpt := GenericAppOption{Type: visType, Order: displayOrder}
	app.AppConfig.Options[OptionAppVisibility] = visOpt
}

func (app *App) SetAppTitle(title string) {
	if title == "" {
		delete(app.AppConfig.Options, OptionTitle)
	} else {
		app.AppConfig.Options[OptionTitle] = GenericAppOption{AppTitle: title}
	}
}

func (app *App) SetHtml(htmlStr string) error {
	bytesReader := bytes.NewReader([]byte(htmlStr))
	blobData, err := BlobDataFromReadSeeker(rootHtmlKey, htmlMimeType, bytesReader)
	if err != nil {
		return err
	}
	err = app.SetRawBlobData(blobData, bytesReader)
	if err != nil {
		return err
	}
	app.SetOption(OptionHtml, GenericAppOption{Type: HtmlTypeStatic})
	return nil
}

func (app *App) SetHtmlFromFile(fileName string) error {
	htmlValue := fileValue(fileName, true)
	htmlIf, err := htmlValue.GetValue()
	if err != nil {
		return err
	}
	htmlStr, err := dashutil.ConvertToString(htmlIf)
	if err != nil {
		return err
	}
	bytesReader := bytes.NewReader([]byte(htmlStr))
	blobData, err := BlobDataFromReadSeeker(rootHtmlKey, htmlMimeType, bytesReader)
	if err != nil {
		return err
	}
	err = app.SetRawBlobData(blobData, bytesReader)
	if err != nil {
		return err
	}
	app.appRuntime.html = htmlValue
	app.SetOption(OptionHtml, GenericAppOption{Type: HtmlTypeDynamicWhenConnected})
	return nil
}

func (apprt *AppRuntimeImpl) SetAppStateType(appStateType reflect.Type) {
	apprt.appStateType = appStateType
}

// initType is either InitHandlerRequired, InitHandlerRequiredWhenConnected, or InitHandlerNone
func (app *App) SetInitHandlerType(initType string) {
	app.SetOption(OptionInitHandler, GenericAppOption{Type: initType})
}

func (apprt *AppRuntimeImpl) GetAppName() string {
	return apprt.appName
}

func (apprt *AppRuntimeImpl) AddRawMiddleware(name string, mwFunc MiddlewareFuncType, priority float64) {
	apprt.RemoveMiddleware(name)
	apprt.lock.Lock()
	defer apprt.lock.Unlock()
	newmws := make([]middlewareType, len(apprt.middlewares)+1)
	copy(newmws, apprt.middlewares)
	newmws[len(apprt.middlewares)] = middlewareType{Name: name, Fn: mwFunc, Priority: priority}
	sort.Slice(newmws, func(i int, j int) bool {
		mw1 := newmws[i]
		mw2 := newmws[j]
		return mw1.Priority > mw2.Priority
	})
	apprt.middlewares = newmws
}

func (apprt *AppRuntimeImpl) RemoveMiddleware(name string) {
	apprt.lock.Lock()
	defer apprt.lock.Unlock()
	mws := make([]middlewareType, 0)
	for _, mw := range apprt.middlewares {
		if mw.Name == name {
			continue
		}
		mws = append(mws, mw)
	}
	apprt.middlewares = mws
}

func (apprt *AppRuntimeImpl) SetRawHandler(path string, handlerFn func(req *Request) (interface{}, error)) error {
	if !dashutil.IsHandlerPathValid(path) {
		return fmt.Errorf("Invalid handler path")
	}
	apprt.setHandler(path, handlerType{HandlerFn: handlerFn})
	return nil
}

func (apprt *AppRuntimeImpl) SetInitHandler(handlerFn func(req *Request) error) error {
	apprt.setHandler(handlerPathInit, handlerType{HandlerFn: wrapHandler(handlerFn)})
	return nil
}

func (app *AppRuntimeImpl) htmlHandler(req *Request) (interface{}, error) {
	if app.html == nil {
		return nil, nil
	}
	htmlValueIf, err := app.html.GetValue()
	if err != nil {
		return nil, err
	}
	htmlStr, err := dashutil.ConvertToString(htmlValueIf)
	if err != nil {
		return nil, err
	}
	req.setHtml(htmlStr)
	return nil, nil
}

func (app *App) GetAppName() string {
	return app.AppConfig.AppName
}

// SetRawBlobData blobData must have BlobKey, MimeType, Size, and Sha256 set.
// Clients will normally call SetBlobDataFromFile, or construct a BlobData
// from calling BlobDataFromReadSeeker or BlobDataFromReader rather than
// creating a BlobData directly.
func (app *App) SetRawBlobData(blobData BlobData, reader io.Reader) error {
	err := app.api.SetBlobData(app.AppConfig, blobData, reader)
	if err != nil {
		log.Printf("Dashborg error setting blob data app:%s blobkey:%s err:%v\n", app.AppConfig.AppName, blobData.ExtBlobKey(), err)
		return err
	}
	return nil
}

// Will call Seek(0, 0) on the reader twice, once at the beginning and once at the end.
// If an error is returned, the seek position is not specified.  If no error is returned
// the reader will be reset to the beginning.
// A []byte can be wrapped in a bytes.Buffer to use this function (error will always be nil)
func BlobDataFromReadSeeker(extBlobKey string, mimeType string, r io.ReadSeeker) (BlobData, error) {
	blobNs, blobKey, err := dashutil.ParseExtBlobKey(extBlobKey)
	if err != nil {
		return BlobData{}, dasherr.ValidateErr(fmt.Errorf("Invalid BlobKey"))
	}
	if blobNs == "" {
		blobNs = appBlobNs
	}
	_, err = r.Seek(0, 0)
	if err != nil {
		return BlobData{}, nil
	}
	h := sha256.New()
	numCopyBytes, err := io.Copy(h, r)
	if err != nil {
		return BlobData{}, err
	}
	hashVal := h.Sum(nil)
	hashValStr := base64.StdEncoding.EncodeToString(hashVal[:])
	_, err = r.Seek(0, 0)
	if err != nil {
		return BlobData{}, err
	}
	blobData := BlobData{
		BlobNs:   blobNs,
		BlobKey:  blobKey,
		MimeType: mimeType,
		Sha256:   hashValStr,
		Size:     numCopyBytes,
	}
	return blobData, nil
}

// If you only have an io.Reader, this function will call ioutil.ReadAll, read the full stream
// into a []byte, compute the size and SHA-256, and then wrap the []byte in a *bytes.Reader
// suitable to pass to SetRawBlobData()
func BlobDataFromReader(extBlobKey string, mimeType string, r io.Reader) (BlobData, *bytes.Reader, error) {
	barr, err := ioutil.ReadAll(r)
	if err != nil {
		return BlobData{}, nil, err
	}
	breader := bytes.NewReader(barr)
	blobData, err := BlobDataFromReadSeeker(extBlobKey, mimeType, breader)
	if err != nil {
		return BlobData{}, nil, err
	}
	return blobData, breader, nil
}

func (app *App) SetBlobDataFromFile(key string, mimeType string, fileName string, metadata interface{}) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	blobData, err := BlobDataFromReadSeeker(key, mimeType, fd)
	if err != nil {
		return err
	}
	blobData.Metadata = metadata
	return app.SetRawBlobData(blobData, fd)
}

func (app *App) SetJsonBlob(extBlobKey string, data interface{}, metadata interface{}) error {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(data)
	if err != nil {
		return dasherr.JsonMarshalErr("BlobData", err)
	}
	reader := bytes.NewReader(jsonBuf.Bytes())
	blob, err := BlobDataFromReadSeeker(extBlobKey, jsonMimeType, reader)
	if err != nil {
		return err
	}
	blob.Metadata = metadata
	return app.SetRawBlobData(blob, reader)
}

func (app *App) RemoveBlob(extBlobKey string) error {
	blobNs, blobKey, err := dashutil.ParseExtBlobKey(extBlobKey)
	if err != nil {
		return dasherr.ValidateErr(fmt.Errorf("Invalid BlobKey"))
	}
	if blobNs == "" {
		blobNs = appBlobNs
	}
	blobData := BlobData{
		BlobNs:  blobNs,
		BlobKey: blobKey,
	}
	return app.api.RemoveBlob(app.AppConfig, blobData)
}
