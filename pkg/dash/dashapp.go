package dash

import (
	"fmt"
	"strings"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
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
}

type middlewareType struct {
	Name     string
	Fn       MiddlewareFuncType
	Priority float64
}

type MiddlewareNextFuncType func(req *AppRequest) (interface{}, error)
type MiddlewareFuncType func(req *AppRequest, nextFn MiddlewareNextFuncType) (interface{}, error)

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
	htmlStr           string
	htmlFileName      string
	htmlFileWatchOpts *WatchOpts
	htmlFromRuntime   bool
	htmlExtPath       string
	errs              []error
}

func (app *App) Runtime() *AppRuntimeImpl {
	return app.appRuntime
}

func (app *App) SetRuntime(apprt *AppRuntimeImpl) {
	if apprt == nil {
		apprt = MakeAppRuntime()
	}
	app.appRuntime = apprt
}

func MakeApp(appNameOrPath string) *App {
	appName, appPath, appNameErr := dashutil.ResolveAppNameOrPath(appNameOrPath)
	rtn := &App{
		appPath:    appPath,
		appRuntime: MakeAppRuntime(),
		appConfig: AppConfig{
			ClientVersion: ClientVersion,
			AppName:       appName,
			AllowedRoles:  []string{RoleUser},
		},
	}
	if appNameErr != nil {
		rtn.errs = append(rtn.errs, dasherr.ValidateErr(fmt.Errorf("MakeApp: %w", appNameErr)))
	}
	return rtn
}

func MakeAppFromConfig(appNameOrPath string, cfg AppConfig) *App {
	appName, appPath, appNameErr := dashutil.ResolveAppNameOrPath(appNameOrPath)
	cfg.AppName = appName
	cfg.ClientVersion = ClientVersion
	if len(cfg.AllowedRoles) == 0 {
		cfg.AllowedRoles = []string{RoleUser}
	}
	rtn := &App{
		appPath:    appPath,
		appRuntime: MakeAppRuntime(),
		appConfig:  cfg,
	}
	if appNameErr != nil {
		rtn.errs = append(rtn.errs, dasherr.ValidateErr(fmt.Errorf("MakeApp: %w", appNameErr)))
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

func (app *App) AppConfig() (AppConfig, error) {
	if len(app.errs) > 0 {
		return AppConfig{}, app.Err()
	}
	err := app.validateHtmlOpts()
	if err != nil {
		return AppConfig{}, err
	}
	app.appConfig.HtmlPath = app.getHtmlPath()
	app.appConfig.RuntimePath = app.getRuntimePath()
	app.appConfig.ClientVersion = ClientVersion
	return app.appConfig, nil
}

func (config *AppConfig) Validate() error {
	_, _, _, err := dashutil.ParseFullPath(config.HtmlPath, true)
	if err != nil {
		return dasherr.ValidateErr(err)
	}
	_, _, _, err = dashutil.ParseFullPath(config.RuntimePath, false)
	if err != nil {
		return dasherr.ValidateErr(err)
	}
	if !dashutil.IsClientVersionValid(config.ClientVersion) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid ClientVersion"))
	}
	if !dashutil.IsRoleListValid(strings.Join(config.AllowedRoles, ",")) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid AllowedRoles"))
	}
	if len(config.AppTitle) > 80 {
		return dasherr.ValidateErr(fmt.Errorf("AppTitle too long"))
	}
	if len(config.AllowedRoles) == 0 {
		return dasherr.ValidateErr(fmt.Errorf("AllowedRoles cannot be empty"))
	}
	return nil
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

func (app *App) ClearHtml() {
	app.htmlStr = ""
	app.htmlFileName = ""
	app.htmlFileWatchOpts = nil
	app.htmlFromRuntime = false
	app.htmlExtPath = ""
	app.appConfig.HtmlPath = ""
}

func (app *App) SetHtml(htmlStr string) {
	app.ClearHtml()
	app.htmlStr = htmlStr
	return
}

func (app *App) SetHtmlFromFile(fileName string) {
	app.ClearHtml()
	app.htmlFileName = fileName
	return
}

func (app *App) WatchHtmlFile(fileName string, watchOpts *WatchOpts) {
	app.ClearHtml()
	app.htmlFileName = fileName
	if watchOpts == nil {
		watchOpts = &WatchOpts{}
	}
	app.htmlFileWatchOpts = watchOpts
	return
}

func (app *App) SetHtmlExternalPath(path string) {
	app.ClearHtml()
	app.htmlExtPath = path
}

func (app *App) SetHtmlFromRuntime() {
	app.ClearHtml()
	app.htmlFromRuntime = true
}

// initType is either InitHandlerRequired, InitHandlerRequiredWhenConnected, or InitHandlerNone
func (app *App) SetInitRequired(initRequired bool) {
	app.appConfig.InitRequired = initRequired
}

func (app *App) AppName() string {
	return app.appConfig.AppName
}

func (app *App) validateHtmlOpts() error {
	var htmlTypes []string
	if app.htmlStr != "" {
		htmlTypes = append(htmlTypes, fmt.Sprintf("static-html [len=%d]", len(app.htmlStr)))
	}
	if app.htmlFileName != "" {
		htmlTypes = append(htmlTypes, fmt.Sprintf("html file [%s]", app.htmlFileName))
	}
	if app.htmlExtPath != "" {
		htmlTypes = append(htmlTypes, fmt.Sprintf("html path [%s]", app.htmlExtPath))
	}
	if app.htmlFromRuntime {
		htmlTypes = append(htmlTypes, fmt.Sprintf("html from runtime"))
	}
	if len(htmlTypes) >= 2 {
		return dasherr.ValidateErr(fmt.Errorf("Invalid App HTML configuration (multiple conflicting types): %s", strings.Join(htmlTypes, ", ")))
	}
	return nil
}

func (app *App) getHtmlPath() string {
	if app.htmlStr != "" || app.htmlFileName != "" {
		return fmt.Sprintf("%s/html", app.appPath)
	}
	if app.htmlFromRuntime {
		return fmt.Sprintf("%s:@html", app.getRuntimePath())
	}
	if app.htmlExtPath != "" {
		return app.htmlExtPath
	}
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

func (app *App) Err() error {
	var errs []error
	errs = append(app.errs, app.appRuntime.errs...)
	return dashutil.ConvertErrArray(errs)
}

func (app *App) AppPath() string {
	return app.appPath
}
