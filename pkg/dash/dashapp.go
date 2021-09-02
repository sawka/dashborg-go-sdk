package dash

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"

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
	requestTypePath    = "path"
)

const (
	OptionInitHandler   = "inithandler"
	OptionAuth          = "auth"
	OptionOfflineMode   = "offlinemode"
	OptionTitle         = "title"
	OptionAppVisibility = "visibility"

	AuthTypeZone = "zone"

	VisTypeHidden        = "hidden"  // always hide
	VisTypeDefault       = "default" // shown if user has permission
	VisTypeAlwaysVisible = "visible" // always show

	InitHandlerRequired              = "required"
	InitHandlerRequiredWhenConnected = "required-when-connected"
	InitHandlerNone                  = "none"

	OfflineModeEnable  = "enable"
	OfflineModeDisable = "disable"
)

// AppConfig is passed as JSON to the container.  this struct
// helps with marshaling/unmarshaling the structure.
type AppConfig struct {
	AppName       string                      `json:"appname"`
	AppVersion    string                      `json:"appversion,omitempty"` // uuid
	UpdatedTs     int64                       `json:"updatedts"`            // set by container
	ProcRunId     string                      `json:"procrunid"`            // set by container
	ClientVersion string                      `json:"clientversion"`
	Options       map[string]GenericAppOption `json:"options"`
	HtmlPath      string                      `json:"htmlpath"`
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

type MiddlewareNextFuncType func(req *AppRequest) (interface{}, error)
type MiddlewareFuncType func(req *AppRequest, nextFn MiddlewareNextFuncType) (interface{}, error)

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

func MakeApp(appName string, api InternalApi) *App {
	rtn := &App{
		api:        api,
		appRuntime: MakeAppRuntime(appName),
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
		appRuntime: MakeAppRuntime(cfg.AppName),
		appConfig:  cfg,
	}
	rtn.appConfig.AppVersion = uuid.New().String()
	return rtn
}

func (app *App) AppConfig() AppConfig {
	return app.appConfig
}

func (app *App) RawOptionRemove(optName string) {
	delete(app.appConfig.Options, optName)
}

func (app *App) RawOptionSet(optName string, opt GenericAppOption) {
	app.appConfig.Options[optName] = opt
}

func wrapHandler(handlerFn func(req *AppRequest) error) func(req *AppRequest) (interface{}, error) {
	wrappedHandlerFn := func(req *AppRequest) (interface{}, error) {
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

func (app *App) GetAllowedRoles() []string {
	authOpt := app.getAuthOpt()
	return authOpt.AllowedRoles
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

func (app *App) SetHtml(htmlStr string) error {
	bytesReader := bytes.NewReader([]byte(htmlStr))
	fileOpts := &FileOpts{MimeType: MimeTypeDashborgHtml, MkDirs: true}
	fileOpts.AllowedRoles = app.getAuthOpt().AllowedRoles
	err := UpdateFileOptsFromReadSeeker(bytesReader, fileOpts)
	if err != nil {
		return err
	}
	htmlPath := fmt.Sprintf("/@app/%s/html", app.AppName())
	dashfs := &fsImpl{app.api}
	err = dashfs.SetRawPath(htmlPath, bytesReader, fileOpts)
	if err != nil {
		return err
	}
	app.appConfig.HtmlPath = htmlPath
	return nil
}

func (app *App) SetHtmlFromFile(fileName string) error {
	dashfs := &fsImpl{app.api}
	htmlPath := fmt.Sprintf("/@app/%s/html", app.AppName())
	fileOpts := &FileOpts{MimeType: MimeTypeDashborgHtml, MkDirs: true}
	err := dashfs.WatchFile(htmlPath, fileName, fileOpts, nil)
	if err != nil {
		return err
	}
	app.appConfig.HtmlPath = htmlPath
	return nil
}

func (app *App) SetHtmlPath(path string) {
	app.appConfig.HtmlPath = path
}

func (app *App) SetHtmlFromRuntime() {
	app.appConfig.HtmlPath = fmt.Sprintf("/@app/%s/runtime:@html", app.AppName())
}

// initType is either InitHandlerRequired, InitHandlerRequiredWhenConnected, or InitHandlerNone
func (app *App) SetInitHandlerType(initType string) {
	app.RawOptionSet(OptionInitHandler, GenericAppOption{Type: initType})
}

func (app *App) AppName() string {
	return app.appConfig.AppName
}
