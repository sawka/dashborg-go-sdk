package dash

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
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

	AppVersion string `json:"appversion,omitempty"` // uuid
	UpdatedTs  int64  `json:"updatedts"`            // set by container
	ProcRunId  string `json:"procrunid"`            // set by container

	// helpers for WriteApp, if these are set, WriteApp will call the
	// approritate SetPath functions to write the HTML to HtmlPath
	HtmlStr           string     `json:"-"`
	HtmlFileName      string     `json:"-"`
	HtmlFileWatchOpts *WatchOpts `json:"-"`
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
	isNewApp          bool
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

func MakeApp(appNameOrPath string) *App {
	appName, appPath, appNameErr := dashutil.ResolveAppNameOrPath(appNameOrPath)
	rtn := &App{
		appPath:    appPath,
		appRuntime: MakeAppRuntime(),
		appConfig: AppConfig{
			ClientVersion: ClientVersion,
			AppVersion:    uuid.New().String(),
			AppName:       appName,
			AllowedRoles:  []string{RoleUser},
		},
		isNewApp: true,
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

func (app *App) IsNew() bool {
	return app.isNewApp
}

func MakeAppFromConfig(cfg AppConfig) *App {
	rtn := &App{
		appRuntime: MakeAppRuntime(),
		appConfig:  cfg,
	}
	rtn.appConfig.AppVersion = uuid.New().String()
	return rtn
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
	app.appConfig.HtmlStr = app.htmlStr
	app.appConfig.HtmlFileName = app.htmlFileName
	app.appConfig.HtmlFileWatchOpts = app.htmlFileWatchOpts
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

func (app *App) clearHtml() {
	app.htmlStr = ""
	app.htmlFileName = ""
	app.htmlFileWatchOpts = nil
	app.htmlFromRuntime = false
	app.htmlExtPath = ""
	app.appConfig.HtmlPath = ""
}

func (app *App) SetHtml(htmlStr string) {
	app.clearHtml()
	app.htmlStr = htmlStr
	return
}

func (app *App) SetHtmlFromFile(fileName string) {
	app.clearHtml()
	app.htmlFileName = fileName
	return
}

func (app *App) WatchHtmlFile(fileName string, watchOpts *WatchOpts) {
	app.clearHtml()
	app.htmlFileName = fileName
	if watchOpts == nil {
		watchOpts = &WatchOpts{}
	}
	app.htmlFileWatchOpts = watchOpts
	return
}

func (app *App) SetHtmlExternalPath(path string) {
	app.clearHtml()
	app.htmlExtPath = path
}

func (app *App) SetHtmlFromRuntime() {
	app.clearHtml()
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

func (acfg AppConfig) AppPath() string {
	return fmt.Sprintf("/@app/%s", acfg.AppName)
}
