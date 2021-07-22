package dash

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"sync"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

var notAuthorizedErr = fmt.Errorf("Not Authorized")

const MaxAppConfigSize = 1000000

const (
	OptionOnLoadHandler = "onloadhandler"
	OptionHtml          = "html"
	OptionAuth          = "auth"
	OptionOfflineMode   = "offlinemode"

	AuthTypeZone    = "zone"
	AuthTypeZoneApp = "zone-app"
	AuthTypeAppOnly = "app-only"
	AuthTypePublic  = "public"
)

// html: static, dynamic, dynamic-when-connected
// offlinemode: enable, disable

// AppConfig is passed as JSON to the container.  this struct
// helps with marshaling/unmarshaling the structure.
type AppConfig struct {
	AppName            string                      `json:"appname"`
	AppVersion         string                      `json:"appversion,omitempty"` // uuid
	UpdatedTs          int64                       `json:"updatedts"`            // set by container
	ProcRunId          string                      `json:"procrunid"`            // set by container
	ClientVersion      string                      `json:"clientversion"`
	Options            map[string]GenericAppOption `json:"options"`
	StaticHtml         string                      `json:"statichtml,omitempty"`
	StaticData         []staticDataVal             `json:"staticdata,omitempty"`
	ClearExistingData  bool                        `json:"clearexistingdata,omitempty"`
	ClearExistingBlobs bool                        `json:"clearexistingblobs,omitempty"`
}

// super-set of all option fields for JSON marshaling/parsing
type GenericAppOption struct {
	Type         string   `json:"type,omitempty"`
	Path         string   `json:"path,omitempty"`
	AllowedRoles []string `json:"allowedroles,omitempty"`
}

type staticDataVal struct {
	Path string      `json:"path"`
	Data interface{} `json:"data"`
}

type BlobData struct {
	BlobKey  string      `json:"blobkey"`
	MimeType string      `json:"mimetype"`
	Size     int64       `json:"size"`
	UpdateTs int64       `json:"updatets"`
	Metadata interface{} `json:"metadata"`
	Removed  bool        `json:"removed"`
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
	GetAppConfig() AppConfig
	RunHandler(req *Request) (interface{}, error)
}

type appRuntimeImpl struct {
	lock         *sync.Mutex
	appStateType reflect.Type
	html         valueType
	handlers     map[handlerKey]handlerType
}

type App struct {
	AppConfig  AppConfig
	appRuntime *appRuntimeImpl
	Container  Container

	// liveUpdateMode  bool
	// connectOnlyMode bool
}

type BlobManager interface {
	// BlobBucket() string
	SetBlobData(key string, mimeType string, reader io.Reader, metadata interface{}) error
	SetBlobDataFromFile(key string, mimeType string, fileName string, metadata interface{}) error
	// ClearBlob(key string)
	// ListBlobs() map[string]BlobData
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

func makeAppRuntime() *appRuntimeImpl {
	rtn := &appRuntimeImpl{
		lock: &sync.Mutex{},
	}
	rtn.handlers = make(map[handlerKey]handlerType)
	rtn.handlers[handlerKey{HandlerType: "html"}] = handlerType{HandlerFn: rtn.htmlHandler}
	return rtn
}

func MakeApp(appName string, container Container) *App {
	rtn := &App{
		Container:  container,
		appRuntime: makeAppRuntime(),
		AppConfig: AppConfig{
			AppVersion: uuid.New().String(),
			AppName:    appName,
		},
	}
	rtn.AppConfig.Options = make(map[string]GenericAppOption)
	authOpt := defaultAuthOpt()
	rtn.AppConfig.Options[OptionAuth] = authOpt
	rtn.AppConfig.Options[OptionOfflineMode] = GenericAppOption{Type: "allow"}
	return rtn
}

// func (app *App) SetConnectOnly(connectOnly bool) {
// 	app.connectOnlyMode = connectOnly
// }

// func (app *App) SetLiveUpdate(liveUpdate bool) {
// 	app.liveUpdateMode = liveUpdate
// }

func (app *App) ClearExistingData() {
	app.AppConfig.ClearExistingData = true
}

func (app *App) ClearExistingBlobs() {
	app.AppConfig.ClearExistingBlobs = true
}

func MakeAppFromConfig(cfg AppConfig, container Container) *App {
	rtn := &App{
		Container:  container,
		appRuntime: makeAppRuntime(),
		AppConfig:  cfg,
	}
	rtn.AppConfig.AppVersion = uuid.New().String()
	return rtn
}

type handlerType struct {
	HandlerFn       func(req *Request) (interface{}, error)
	BoundHandlerKey *handlerKey
}

func (app *App) GetAppConfig() AppConfig {
	return app.AppConfig
}

func (app *App) RunHandler(req *Request) (interface{}, error) {
	return app.appRuntime.RunHandler(req)
}

func (app *appRuntimeImpl) SetHandler(hkey handlerKey, handler handlerType) {
	app.lock.Lock()
	defer app.lock.Unlock()
	app.handlers[hkey] = handler
}

func (app *appRuntimeImpl) RunHandler(req *Request) (interface{}, error) {
	hkey := handlerKey{
		HandlerType: req.info.RequestType,
		Path:        req.info.Path,
	}
	app.lock.Lock()
	hval, ok := app.handlers[hkey]
	app.lock.Unlock()
	if !ok {
		return nil, fmt.Errorf("No handler found for %s:%s", req.info.AppName, req.info.Path)
	}
	rtn, err := hval.HandlerFn(req)
	if err != nil {
		return nil, err
	}
	return rtn, nil
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

func (app *App) CustomAuthHandler(authHandler func(req *Request) error) {
	authOpt := app.getAuthOpt()
	if authOpt.Type == AuthTypeZone {
		authOpt.Type = AuthTypeZoneApp
		app.AppConfig.Options[OptionAuth] = authOpt
	}
	app.appRuntime.SetHandler(handlerKey{HandlerType: "auth"}, handlerType{HandlerFn: wrapHandler(authHandler)})
}

func (app *App) getAuthOpt() GenericAppOption {
	authOpt, ok := app.AppConfig.Options[OptionAuth]
	if !ok {
		return defaultAuthOpt()
	}
	return authOpt
}

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

func (app *App) SetHtml(html string) {
	app.SetOption(OptionHtml, GenericAppOption{
		Type: "static",
	})
	app.AppConfig.StaticHtml = html
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
	app.appRuntime.html = htmlValue
	app.SetOption(OptionHtml, GenericAppOption{Type: "dynamic-when-connected"})
	app.AppConfig.StaticHtml = htmlStr
	return nil
}

func (app *App) SetAppStateType(appStateType reflect.Type) {
	app.appRuntime.appStateType = appStateType
}

func (app *App) SetOnLoadHandler(path string) {
	app.SetOption(OptionOnLoadHandler, GenericAppOption{Path: path})
}

func (app *App) Handler(path string, handlerFn func(req *Request) error) error {
	hkey := handlerKey{HandlerType: "handler", Path: path}
	app.appRuntime.SetHandler(hkey, handlerType{HandlerFn: wrapHandler(handlerFn)})
	return nil
}

func (app *appRuntimeImpl) htmlHandler(req *Request) (interface{}, error) {
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
	RequestEx{req}.SetHtml(htmlStr)
	return nil, nil
}

func (app *App) DataHandler(path string, handlerFn func(req *Request) (interface{}, error)) error {
	hkey := handlerKey{HandlerType: "data", Path: path}
	app.appRuntime.SetHandler(hkey, handlerType{HandlerFn: handlerFn})
	return nil
}

func (app *App) SetStaticData(path string, data interface{}) {
	app.AppConfig.StaticData = append(app.AppConfig.StaticData, staticDataVal{Path: path, Data: data})
}

func (app *App) GetAppName() string {
	return app.AppConfig.AppName
}

func (app *App) SetBlobData(key string, mimeType string, reader io.Reader, metadata interface{}) error {
	blob := BlobData{
		BlobKey:  key,
		MimeType: mimeType,
		UpdateTs: dashutil.Ts(),
		Metadata: metadata,
	}
	err := app.Container.SetBlobData(app.AppConfig, blob, reader)
	if err != nil {
		log.Printf("Dashborg error setting blob data app:%s blobkey:%s err:%v\n", app.AppConfig.AppName, key, err)
		return err
	}
	return nil
}

func (app *App) SetBlobDataFromFile(key string, mimeType string, fileName string, metadata interface{}) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	return app.SetBlobData(key, mimeType, fd, metadata)
}
