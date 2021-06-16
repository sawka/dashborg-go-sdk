package dash

import (
	"fmt"
	"io/ioutil"
	"os"
	"sync"

	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const (
	APP_GUI         = "gui"
	APP_DATASERVICE = "dataservice"
)

type AppConfig struct {
	AppName string
	Options map[string]interface{}
}

type App interface {
	SetHtml(html string)
	SetHtmlFromFile(fileName string)
	SetOnLoadHandler(path string)
	SetAuth(allowedAuths ...AllowedAuth)
	SetOption(opt AppOption)
	RemoveOption(optName string)

	AppHandler(path string, handlerFn func(req *PanelRequest) error) error
	AppHandlerEx(path string, handlerFn interface{}) error
	DataHandler(path string, handlerFn func(req *PanelRequest) (interface{}, error)) error
	DataHandlerEx(path string, handlerFn interface{}) error

	AppConfig() AppConfig

	RunHandler(req *PanelRequest) (interface{}, error)
	GetAppName() string
	GetClientVersion() string
}

type appImpl struct {
	Lock     *sync.Mutex
	AppName  string
	Html     valueType
	InitFn   func(req *PanelRequest) error
	Handlers map[handlerKey]handlerType
	Options  map[string]AppOption
	Auth     []AllowedAuth
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

func MakeApp(appName string) App {
	rtn := &appImpl{
		Lock:    &sync.Mutex{},
		AppName: appName,
	}
	rtn.Handlers = make(map[handlerKey]handlerType)
	rtn.Options = make(map[string]AppOption)
	rtn.Handlers[handlerKey{HandlerType: "auth"}] = handlerType{HandlerFn: rtn.authHandler}
	return rtn
}

type handlerType struct {
	HandlerFn       func(req *PanelRequest) (interface{}, error)
	BoundHandlerKey *handlerKey
}

func (app *appImpl) AppConfig() AppConfig {
	app.Lock.Lock()
	defer app.Lock.Unlock()
	rtn := AppConfig{AppName: app.AppName}
	rtn.Options = make(map[string]interface{})
	for name, opt := range app.Options {
		if name != opt.OptionName() {
			panic(fmt.Sprintf("OptionName does not match hash key: %s:%s %v\n", name, opt.OptionName(), opt))
		}
		rtn.Options[name] = opt.OptionData()
	}
	return rtn
}

func (app *appImpl) RemoveOption(optName string) {
	app.Lock.Lock()
	defer app.Lock.Unlock()

	delete(app.Options, optName)
}

func (app *appImpl) SetOption(opt AppOption) {
	app.Lock.Lock()
	defer app.Lock.Unlock()

	app.Options[opt.OptionName()] = opt
}

func (app *appImpl) setOption_nolock(opt AppOption) {
	app.Options[opt.OptionName()] = opt
}

func (app *appImpl) SetAuth(allowedAuths ...AllowedAuth) {
	app.Lock.Lock()
	defer app.Lock.Unlock()

	app.Auth = allowedAuths
	app.setOption_nolock(AuthOption{Type: "dynamic"})
}

func (app *appImpl) SetHtml(html string) {
	app.Lock.Lock()
	defer app.Lock.Unlock()

	app.Html = interfaceValue(html)
	htmlKey := handlerKey{HandlerType: "html", Path: ""}
	app.Handlers[htmlKey] = handlerType{HandlerFn: app.htmlHandler}
	app.setOption_nolock(HtmlOption{Type: "dynamic"})
}

func (app *appImpl) SetHtmlFromFile(fileName string) {
	app.Lock.Lock()
	defer app.Lock.Unlock()

	app.Html = fileValue(fileName, true)
	htmlKey := handlerKey{HandlerType: "html", Path: ""}
	app.Handlers[htmlKey] = handlerType{HandlerFn: app.htmlHandler}
	app.setOption_nolock(HtmlOption{Type: "dynamic"})
}

func (app *appImpl) SetOnLoadHandler(path string) {
	app.SetOption(OnloadHandlerOption{Path: path})
}

func (app *appImpl) AppHandler(path string, handlerFn func(req *PanelRequest) error) error {
	hkey := handlerKey{HandlerType: "handler", Path: path}
	wrappedHandlerFn := func(req *PanelRequest) (interface{}, error) {
		err := handlerFn(req)
		return nil, err
	}
	app.Handlers[hkey] = handlerType{HandlerFn: wrappedHandlerFn}
	return nil
}

func (app *appImpl) htmlHandler(req *PanelRequest) (interface{}, error) {
	if app.Html == nil {
		return nil, nil
	}
	htmlValue, err := app.Html.GetValue()
	if err != nil {
		return nil, err
	}
	req.SetHtml(htmlValue)
	return nil, nil
}

func (app *appImpl) authHandler(req *PanelRequest) (interface{}, error) {
	req.CheckAuth(AuthNone{})
	return nil, nil
}

func (app *appImpl) DataHandler(path string, handlerFn func(req *PanelRequest) (interface{}, error)) error {
	hkey := handlerKey{HandlerType: "data", Path: path}
	app.Handlers[hkey] = handlerType{HandlerFn: handlerFn}
	return nil
}

func (app *appImpl) RunHandler(req *PanelRequest) (interface{}, error) {
	hkey := handlerKey{
		HandlerType: req.RequestType,
		Path:        req.Path,
	}
	app.Lock.Lock()
	hval, ok := app.Handlers[hkey]
	app.Lock.Unlock()
	if !ok {
		return nil, fmt.Errorf("No handler found for %s:%s", req.PanelName, req.Path)
	}
	rtn, err := hval.HandlerFn(req)
	if err != nil {
		return nil, err
	}
	return rtn, nil
}

func (app *appImpl) GetAppName() string {
	return app.AppName
}

func (app *appImpl) GetClientVersion() string {
	return CLIENT_VERSION
}
