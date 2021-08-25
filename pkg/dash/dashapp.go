package dash

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

var notAuthorizedErr = fmt.Errorf("Not Authorized")

const MaxAppConfigSize = 10000
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
	Added    bool        `json:"added"`
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
	AppName() string
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
	appConfig  AppConfig
	appRuntime *AppRuntimeImpl
	api        InternalApi
	isNewApp   bool
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
		appConfig: AppConfig{
			AppVersion: uuid.New().String(),
			AppName:    appName,
		},
		isNewApp: true,
	}
	rtn.appConfig.Options = make(map[string]GenericAppOption)
	authOpt := defaultAuthOpt()
	rtn.appConfig.Options[OptionAuth] = authOpt
	rtn.appConfig.Options[OptionOfflineMode] = GenericAppOption{Type: "allow"}
	return rtn
}

// offline mode type is either OfflineModeEnable or OfflineModeDisable
func (app *App) SetOfflineModeType(offlineModeType string) {
	app.appConfig.Options[OptionOfflineMode] = GenericAppOption{Type: offlineModeType}
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

func MakeAppFromConfig(cfg AppConfig, api InternalApi) *App {
	rtn := &App{
		api:        api,
		appRuntime: makeAppRuntime(cfg.AppName),
		appConfig:  cfg,
	}
	rtn.appConfig.AppVersion = uuid.New().String()
	return rtn
}

type handlerType struct {
	HandlerFn func(req *Request) (interface{}, error)
}

func (app *App) AppConfig() AppConfig {
	return app.appConfig
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

func (app *App) RawOptionRemove(optName string) {
	delete(app.appConfig.Options, optName)
}

func (app *App) RawOptionSet(optName string, opt GenericAppOption) {
	app.appConfig.Options[optName] = opt
}

func wrapHandler(handlerFn func(req *Request) error) func(req *Request) (interface{}, error) {
	wrappedHandlerFn := func(req *Request) (interface{}, error) {
		err := handlerFn(req)
		return nil, err
	}
	return wrappedHandlerFn
}

func (app *App) getAuthOpt() GenericAppOption {
	authOpt, ok := app.appConfig.Options[OptionAuth]
	if !ok {
		return defaultAuthOpt()
	}
	return authOpt
}

// authType must be AuthTypeZone
func (app *App) SetAuthType(authType string) {
	authOpt := app.getAuthOpt()
	authOpt.Type = authType
	app.appConfig.Options[OptionAuth] = authOpt
}

func (app *App) SetAllowedRoles(roles ...string) {
	authOpt := app.getAuthOpt()
	authOpt.AllowedRoles = roles
	app.appConfig.Options[OptionAuth] = authOpt
}

// SetAppVisibility controls whether the app shows in the UI's app-switcher (see VisType constants)
// Apps will be sorted by displayOrder (and then AppTitle).  displayOrder of 0 (the default) will
// sort to the end of the list, not the beginning
// visType is either VisTypeHidden, VisTypeDefault, or VisTypeAlwaysVisible
func (app *App) SetAppVisibility(visType string, displayOrder float64) {
	visOpt := GenericAppOption{Type: visType, Order: displayOrder}
	app.appConfig.Options[OptionAppVisibility] = visOpt
}

func (app *App) SetAppTitle(title string) {
	if title == "" {
		delete(app.appConfig.Options, OptionTitle)
	} else {
		app.appConfig.Options[OptionTitle] = GenericAppOption{AppTitle: title}
	}
}

func (app *App) Blobs() BlobManager {
	return makeAppBlobManager(app, app.api)
}

func (app *App) SetHtml(htmlStr string) error {
	bytesReader := bytes.NewReader([]byte(htmlStr))
	blobData, err := BlobDataFromReadSeeker(rootHtmlKey, htmlMimeType, bytesReader)
	if err != nil {
		return err
	}
	err = app.Blobs().SetRawBlobData(blobData, bytesReader)
	if err != nil {
		return err
	}
	app.RawOptionSet(OptionHtml, GenericAppOption{Type: HtmlTypeStatic})
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
	err = app.Blobs().SetRawBlobData(blobData, bytesReader)
	if err != nil {
		return err
	}
	app.appRuntime.html = htmlValue
	app.RawOptionSet(OptionHtml, GenericAppOption{Type: HtmlTypeDynamicWhenConnected})
	return nil
}

func (apprt *AppRuntimeImpl) SetAppStateType(appStateType reflect.Type) {
	apprt.appStateType = appStateType
}

// initType is either InitHandlerRequired, InitHandlerRequiredWhenConnected, or InitHandlerNone
func (app *App) SetInitHandlerType(initType string) {
	app.RawOptionSet(OptionInitHandler, GenericAppOption{Type: initType})
}

func (apprt *AppRuntimeImpl) AppName() string {
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

func (app *App) AppName() string {
	return app.appConfig.AppName
}
