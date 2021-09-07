package dash

import (
	"fmt"

	"github.com/google/uuid"
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
	VisTypeHidden        = "hidden"  // always hide
	VisTypeDefault       = "default" // shown if user has permission
	VisTypeAlwaysVisible = "visible" // always show
)

// AppConfig is passed as JSON to the container.  this struct
// helps with marshaling/unmarshaling the structure.
type AppConfig struct {
	AppName       string   `json:"appname"`
	ClientVersion string   `json:"clientversion"`
	AppTitle      string   `json:"apptitle"`
	AppVisType    string   `json:"appvistype"`
	AppVisOrder   float64  `json:"appvisorder"`
	AllowedRoles  []string `json:"allowedroles"`
	InitRequired  bool     `json:"initrequired"`
	OfflineAccess bool     `json:"offlineaccess"`
	HtmlPath      string   `json:"htmlpath,omitempty"`    // empty for ./html
	RuntimePath   string   `json:"runtimepath,omitempty"` // empty for ./runtime

	AppVersion string `json:"appversion,omitempty"` // uuid
	UpdatedTs  int64  `json:"updatedts"`            // set by container
	ProcRunId  string `json:"procrunid"`            // set by container
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
	appPath           string
	appConfig         AppConfig
	appRuntime        *AppRuntimeImpl
	api               InternalApi
	isNewApp          bool
	htmlStr           string
	htmlFileName      string
	htmlFileWatchOpts *WatchOpts
}

func (app *App) Runtime() *AppRuntimeImpl {
	return app.appRuntime
}

func MakeApp(appName string, api InternalApi) *App {
	rtn := &App{
		api:        api,
		appPath:    fmt.Sprintf("/@app/%s", appName),
		appRuntime: MakeAppRuntime(),
		appConfig: AppConfig{
			ClientVersion: ClientVersion,
			AppVersion:    uuid.New().String(),
			AppName:       appName,
			AllowedRoles:  []string{RoleUser},
		},
		isNewApp: true,
	}
	return rtn
}

func (app *App) SetExternalAppRuntimePath(runtimePath string) {
	app.appConfig.RuntimePath = runtimePath
}

// offline mode type is either OfflineModeEnable or OfflineModeDisable
func (app *App) SetOfflineAccess(offlineAccess bool) {
	app.appConfig.OfflineAccess = offlineAccess
}

func (app *App) IsNew() bool {
	return app.isNewApp
}

func MakeAppFromConfig(cfg AppConfig, api InternalApi) *App {
	rtn := &App{
		api:        api,
		appRuntime: MakeAppRuntime(),
		appConfig:  cfg,
	}
	rtn.appConfig.AppVersion = uuid.New().String()
	return rtn
}

func (app *App) AppConfig() AppConfig {
	return app.appConfig
}

func wrapHandler(handlerFn func(req *AppRequest) error) func(req *AppRequest) (interface{}, error) {
	wrappedHandlerFn := func(req *AppRequest) (interface{}, error) {
		err := handlerFn(req)
		return nil, err
	}
	return wrappedHandlerFn
}

func (app *App) SetAllowedRoles(roles ...string) {
	app.appConfig.AllowedRoles = roles
}

func (app *App) GetAllowedRoles() []string {
	return app.appConfig.AllowedRoles
}

func (app *App) SetAppTitle(title string) {
	app.appConfig.AppTitle = title
}

// SetAppVisibility controls whether the app shows in the UI's app-switcher (see VisType constants)
// Apps will be sorted by displayOrder (and then AppTitle).  displayOrder of 0 (the default) will
// sort to the end of the list, not the beginning
// visType is either VisTypeHidden, VisTypeDefault, or VisTypeAlwaysVisible
func (app *App) SetAppVisibility(visType string, visOrder float64) {
	app.appConfig.AppVisType = visType
	app.appConfig.AppVisOrder = visOrder
}

func (app *App) SetHtml(htmlStr string) error {
	app.htmlStr = htmlStr
	app.htmlFileName = ""
	app.appConfig.HtmlPath = ""
	return nil
}

func (app *App) SetHtmlFromFile(fileName string) error {
	app.htmlStr = ""
	app.htmlFileName = fileName
	app.appConfig.HtmlPath = ""
	app.htmlFileWatchOpts = nil
	// maybe check file to see if it exists and is readable?
	return nil
}

func (app *App) WatchHtmlFile(fileName string, watchOpts *WatchOpts) error {
	app.htmlStr = ""
	app.htmlFileName = fileName
	app.appConfig.HtmlPath = ""
	if watchOpts == nil {
		watchOpts = &WatchOpts{}
	}
	app.htmlFileWatchOpts = watchOpts
	// maybe check file to see if it exists and is readable?
	return nil
}

func (app *App) SetExternalHtmlPath(path string) {
	app.htmlStr = ""
	app.htmlFileName = ""
	app.appConfig.HtmlPath = path
}

func (app *App) SetHtmlFromRuntime() {
	app.appConfig.HtmlPath = fmt.Sprintf("/@app/%s/runtime:@html", app.AppName())
}

// initType is either InitHandlerRequired, InitHandlerRequiredWhenConnected, or InitHandlerNone
func (app *App) SetInitRequired(initRequired bool) {
	app.appConfig.InitRequired = initRequired
}

func (app *App) AppName() string {
	return app.appConfig.AppName
}

func (app *App) validateHtmlOpts() error {
	if app.htmlStr != "" && app.htmlFileName != "" {
		return fmt.Errorf("Cannot have static-html [len=%d] and an html file set [%s]", len(app.htmlStr), app.htmlFileName)
	}
	if app.htmlStr != "" && app.appConfig.HtmlPath != "" {
		return fmt.Errorf("Cannot have static-html [len=%d] and an external html path set [%s]", len(app.htmlStr), app.appConfig.HtmlPath)
	}
	if app.htmlFileName != "" && app.appConfig.HtmlPath != "" {
		return fmt.Errorf("Cannot have an html file [%s] and an external html path set [%s]", app.htmlFileName, app.appConfig.HtmlPath)
	}
	return nil
}

func (app *App) getHtmlPath() string {
	if app.appConfig.HtmlPath != "" {
		return app.appConfig.HtmlPath
	}
	return fmt.Sprintf("%s/html", app.appPath)
}

func (app *App) getRuntimePath() string {
	if app.appConfig.RuntimePath != "" {
		return app.appConfig.RuntimePath
	}
	return fmt.Sprintf("%s/runtime", app.appPath)
}

func (app *App) hasDefaultRuntimePath() bool {
	return app.getRuntimePath() == fmt.Sprintf("%s/runtime", app.appPath)
}
