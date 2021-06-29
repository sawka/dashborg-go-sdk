package dash

import (
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"sync"

	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

var notAuthorizedErr = fmt.Errorf("Not Authorized")

const (
	AppTypeGUI         = "gui"
	AppTypeDataService = "dataservice"
)

const (
	OptionOnLoadHandler = "onloadhandler"
	OptionHtml          = "html"
	OptionAuth          = "auth"
	OptionAllowedRoles  = "auth-allowedroles"
)

type AppConfig struct {
	AppName string
	AppType string
	Options map[string]interface{}
}

type AppOption interface {
	OptionName() string
	OptionData() interface{}
}

// super-set of all option fields for JSON marshaling/parsing
type GenericAppOption struct {
	Name string `json:"-"` // not marshaled as part of OptionData
	Type string `json:"type,omitempty"`
	Path string `json:"path,omitempty"`

	AllowedRoles []string `json:"allowedroles,omitempty"`
}

func (opt GenericAppOption) OptionName() string {
	return opt.Name
}

func (opt GenericAppOption) OptionData() interface{} {
	return opt
}

type AppRuntime interface {
	AppConfig() AppConfig
	RunHandler(req *Request) (interface{}, error)
	GetAppName() string
	GetClientVersion() string
}

type App struct {
	lock     *sync.Mutex
	appName  string
	appType  string
	html     valueType
	handlers map[handlerKey]handlerType
	options  map[string]AppOption
}

type valueType interface {
	IsDynamic() bool
	GetValue() (string, error)
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

func (fv funcValueType) GetValue() (string, error) {
	return fv.ValueFn()
}

func MakeApp(appName string) *App {
	rtn := &App{
		lock:    &sync.Mutex{},
		appName: appName,
		appType: AppTypeGUI,
	}
	rtn.handlers = make(map[handlerKey]handlerType)
	rtn.options = make(map[string]AppOption)
	rtn.setOption_nolock(GenericAppOption{Name: OptionAuth, Type: "none"})
	rtn.setOption_nolock(GenericAppOption{Name: OptionAllowedRoles, AllowedRoles: []string{"user"}})
	rtn.handlers[handlerKey{HandlerType: "html"}] = handlerType{HandlerFn: rtn.htmlHandler}
	return rtn
}

type handlerType struct {
	HandlerFn       func(req *Request) (interface{}, error)
	BoundHandlerKey *handlerKey
}

func (app *App) AppConfig() AppConfig {
	app.lock.Lock()
	defer app.lock.Unlock()
	rtn := AppConfig{AppName: app.appName, AppType: app.appType}
	rtn.Options = make(map[string]interface{})
	for name, opt := range app.options {
		if name != opt.OptionName() {
			panic(fmt.Sprintf("OptionName does not match hash key: %s:%s %v\n", name, opt.OptionName(), opt))
		}
		rtn.Options[name] = opt.OptionData()
	}
	return rtn
}

func (app *App) RemoveOption(optName string) {
	app.lock.Lock()
	defer app.lock.Unlock()

	delete(app.options, optName)
}

func (app *App) SetOption(opt AppOption) {
	app.lock.Lock()
	defer app.lock.Unlock()

	app.options[opt.OptionName()] = opt
}

func (app *App) setOption_nolock(opt AppOption) {
	app.options[opt.OptionName()] = opt
}

func wrapHandler(handlerFn func(req *Request) error) func(req *Request) (interface{}, error) {
	wrappedHandlerFn := func(req *Request) (interface{}, error) {
		err := handlerFn(req)
		return nil, err
	}
	return wrappedHandlerFn
}

func (app *App) CustomAuthHandler(authHandler func(req *Request) error) {
	app.lock.Lock()
	defer app.lock.Unlock()
	app.handlers[handlerKey{HandlerType: "auth"}] = handlerType{HandlerFn: wrapHandler(authHandler)}
	app.setOption_nolock(GenericAppOption{Name: OptionAuth, Type: "dynamic"})
}

func (app *App) SetAllowedRoles(roles ...string) {
	app.lock.Lock()
	defer app.lock.Unlock()

	app.setOption_nolock(GenericAppOption{Name: OptionAllowedRoles, AllowedRoles: roles})
}

func (app *App) SetHtml(html string) {
	app.lock.Lock()
	defer app.lock.Unlock()

	app.html = stringValue(html)
	app.setOption_nolock(GenericAppOption{Name: OptionHtml, Type: "dynamic"})
}

func (app *App) SetHtmlFromFile(fileName string) {
	app.lock.Lock()
	defer app.lock.Unlock()

	app.html = fileValue(fileName, true)
	app.setOption_nolock(GenericAppOption{Name: OptionHtml, Type: "dynamic"})
}

func (app *App) SetAppStateType(appStateType reflect.Type) {
	return
}

func (app *App) SetOnLoadHandler(path string) {
	app.SetOption(GenericAppOption{Name: OptionOnLoadHandler, Path: path})
}

func (app *App) Handler(path string, handlerFn func(req *Request) error) error {
	hkey := handlerKey{HandlerType: "handler", Path: path}
	app.handlers[hkey] = handlerType{HandlerFn: wrapHandler(handlerFn)}
	return nil
}

func (app *App) htmlHandler(req *Request) (interface{}, error) {
	if app.html == nil {
		return nil, nil
	}
	htmlValue, err := app.html.GetValue()
	if err != nil {
		return nil, err
	}
	RequestEx{req}.SetHtml(htmlValue)
	return nil, nil
}

func (app *App) DataHandler(path string, handlerFn func(req *Request) (interface{}, error)) error {
	hkey := handlerKey{HandlerType: "data", Path: path}
	app.handlers[hkey] = handlerType{HandlerFn: handlerFn}
	return nil
}

func (app *App) RunHandler(req *Request) (interface{}, error) {
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

func (app *App) GetAppName() string {
	return app.appName
}

func (app *App) GetClientVersion() string {
	return ClientVersion
}

func (app *App) SetAppType(appType string) {
	app.appType = appType
}
