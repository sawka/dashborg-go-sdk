package dash

import (
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

type handlerType struct {
	HandlerFn func(req *AppRequest) (interface{}, error)
}

type linkHandlerFn func(req Request) (interface{}, error)

type LinkRuntime interface {
	RunHandler(req Request) (interface{}, error)
}

type LinkRuntimeImpl struct {
	lock     *sync.Mutex
	handlers map[string]linkHandlerFn
}

type AppRuntime interface {
	AppName() string
	RunHandler(req *AppRequest) (interface{}, error)
}

type AppRuntimeImpl struct {
	appName      string
	lock         *sync.Mutex
	appStateType reflect.Type
	html         valueType
	handlers     map[string]handlerType
	middlewares  []middlewareType
}

// Apps that are created using dashcloud.OpenApp() have their own built in runtime.
// Only call MakeAppRuntime when you want to call dashcloud.ConnectAppRuntime()
// without calling OpenApp.
func MakeAppRuntime(appName string) *AppRuntimeImpl {
	rtn := &AppRuntimeImpl{
		appName: appName,
		lock:    &sync.Mutex{},
	}
	rtn.handlers = make(map[string]handlerType)
	rtn.handlers[handlerPathHtml] = handlerType{HandlerFn: rtn.htmlHandler}
	return rtn
}

func (app *AppRuntimeImpl) setHandler(path string, handler handlerType) {
	app.lock.Lock()
	defer app.lock.Unlock()
	app.handlers[path] = handler
}

func (apprt *AppRuntimeImpl) RunHandler(req *AppRequest) (interface{}, error) {
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

func mwHelper(outerReq *AppRequest, hval handlerType, mws []middlewareType, mwPos int) (interface{}, error) {
	if mwPos >= len(mws) {
		return hval.HandlerFn(outerReq)
	}
	mw := mws[mwPos]
	return mw.Fn(outerReq, func(innerReq *AppRequest) (interface{}, error) {
		if innerReq == nil {
			panic("No Request Passed to middleware nextFn")
		}
		return mwHelper(innerReq, hval, mws, mwPos+1)
	})
}

func (apprt *AppRuntimeImpl) SetAppStateType(appStateType reflect.Type) {
	apprt.appStateType = appStateType
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

func (apprt *AppRuntimeImpl) SetRawHandler(path string, handlerFn func(req *AppRequest) (interface{}, error)) error {
	if !dashutil.IsHandlerPathValid(path) {
		return fmt.Errorf("Invalid handler path")
	}
	apprt.setHandler(path, handlerType{HandlerFn: handlerFn})
	return nil
}

func (apprt *AppRuntimeImpl) SetInitHandler(handlerFn func(req *AppRequest) error) error {
	apprt.setHandler(handlerPathInit, handlerType{HandlerFn: wrapHandler(handlerFn)})
	return nil
}

func (app *AppRuntimeImpl) htmlHandler(req *AppRequest) (interface{}, error) {
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

func MakeLinkRuntime() *LinkRuntimeImpl {
	rtn := &LinkRuntimeImpl{
		lock:     &sync.Mutex{},
		handlers: make(map[string]linkHandlerFn),
	}
	return rtn
}

func (linkrt *LinkRuntimeImpl) setHandler(name string, fn linkHandlerFn) {
	linkrt.lock.Lock()
	defer linkrt.lock.Unlock()
	linkrt.handlers[name] = fn
}

func (linkrt *LinkRuntimeImpl) RunHandler(req Request) (interface{}, error) {
	info := req.RequestInfo()
	if info.RequestType != requestTypePath {
		return nil, dasherr.ValidateErr(fmt.Errorf("Invalid RequestType for linked runtime"))
	}
	if info.PathFrag == "" {
		return nil, dasherr.ValidateErr(fmt.Errorf("Invalid Request, no PathFrag set for linked runtime"))
	}
	pathFrag := info.PathFrag
	linkrt.lock.Lock()
	linkfn, ok := linkrt.handlers[pathFrag]
	linkrt.lock.Unlock()
	if !ok {
		return nil, dasherr.ErrWithCode(dasherr.ErrCodeNoHandler, fmt.Errorf("No handler found for %s:%s", info.Path, info.PathFrag))
	}
	return linkfn(req)
}
