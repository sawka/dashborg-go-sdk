package dash

import (
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const (
	pathFragDefault = "@default"
	pathFragInit    = "@init"
	pathFragHtml    = "@html"
)

type appHandlerType struct {
	HandlerFn func(req *AppRequest) (interface{}, error)
}

type linkHandlerType struct {
	HandlerFn func(req Request) (interface{}, error)
}

type LinkRuntime interface {
	RunHandler(req *AppRequest) (interface{}, error)
}

type LinkRuntimeImpl struct {
	lock     *sync.Mutex
	handlers map[string]linkHandlerType
	errs     []error
}

type AppRuntimeImpl struct {
	lock         *sync.Mutex
	appStateType reflect.Type
	handlers     map[string]appHandlerType
	middlewares  []middlewareType
	errs         []error
}

type HasErr interface {
	Err() error
}

// Apps that are created using dashcloud.OpenApp() have their own built in runtime.
// Only call MakeAppRuntime when you want to call dashcloud.ConnectAppRuntime()
// without calling OpenApp.
func MakeAppRuntime() *AppRuntimeImpl {
	rtn := &AppRuntimeImpl{
		lock:     &sync.Mutex{},
		handlers: make(map[string]appHandlerType),
	}
	return rtn
}

func (app *AppRuntimeImpl) setHandler(path string, handler appHandlerType) {
	app.lock.Lock()
	defer app.lock.Unlock()
	app.handlers[path] = handler
}

func (apprt *AppRuntimeImpl) RunHandler(req *AppRequest) (interface{}, error) {
	pathFrag := req.info.PathFrag
	if pathFrag == "" {
		return nil, dasherr.ValidateErr(fmt.Errorf("PathFrag cannot be empty for linked request"))
	}
	apprt.lock.Lock()
	hval, ok := apprt.handlers[pathFrag]
	mws := apprt.middlewares
	apprt.lock.Unlock()
	if !ok {
		return nil, fmt.Errorf("No handler found for %s", req.RequestInfo().FullPath())
	}
	rtn, err := mwHelper(req, hval, mws, 0)
	if err != nil {
		return nil, err
	}
	return rtn, nil
}

func mwHelper(outerReq *AppRequest, hval appHandlerType, mws []middlewareType, mwPos int) (interface{}, error) {
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

func (apprt *AppRuntimeImpl) SetRawHandler(handlerName string, handlerFn func(req *AppRequest) (interface{}, error)) error {
	if !dashutil.IsPathFragValid(handlerName) {
		return fmt.Errorf("Invalid handler name")
	}
	apprt.setHandler(handlerName, appHandlerType{HandlerFn: handlerFn})
	return nil
}

func (apprt *AppRuntimeImpl) SetInitHandler(handlerFn interface{}) {
	apprt.Handler(pathFragInit, handlerFn)
}

func (apprt *AppRuntimeImpl) SetHtmlHandler(handlerFn interface{}) {
	apprt.Handler(pathFragHtml, handlerFn)
}

func MakeRuntime() *LinkRuntimeImpl {
	rtn := &LinkRuntimeImpl{
		lock:     &sync.Mutex{},
		handlers: make(map[string]linkHandlerType),
	}
	return rtn
}

func MakeSingleFnRuntime(handlerFn interface{}) *LinkRuntimeImpl {
	rtn := &LinkRuntimeImpl{
		lock:     &sync.Mutex{},
		handlers: make(map[string]linkHandlerType),
	}
	rtn.Handler(pathFragDefault, handlerFn)
	return rtn
}

func (linkrt *LinkRuntimeImpl) setHandler(name string, fn linkHandlerType) {
	linkrt.lock.Lock()
	defer linkrt.lock.Unlock()
	linkrt.handlers[name] = fn
}

func (linkrt *LinkRuntimeImpl) RunHandler(req *AppRequest) (interface{}, error) {
	info := req.RequestInfo()
	if info.RequestType != requestTypePath {
		return nil, dasherr.ValidateErr(fmt.Errorf("Invalid RequestType for linked runtime"))
	}
	pathFrag := info.PathFrag
	if pathFrag == "" {
		return nil, dasherr.ValidateErr(fmt.Errorf("PathFrag cannot be empty for linked request"))
	}
	linkrt.lock.Lock()
	linkfn, ok := linkrt.handlers[pathFrag]
	linkrt.lock.Unlock()
	if !ok {
		return nil, dasherr.ErrWithCode(dasherr.ErrCodeNoHandler, fmt.Errorf("No handler found for %s:%s", info.Path, info.PathFrag))
	}
	return linkfn.HandlerFn(req)
}

func (linkrt *LinkRuntimeImpl) SetRawHandler(handlerName string, handlerFn func(req Request) (interface{}, error)) error {
	if !dashutil.IsPathFragValid(handlerName) {
		return fmt.Errorf("Invalid handler name")
	}
	linkrt.setHandler(handlerName, linkHandlerType{HandlerFn: handlerFn})
	return nil
}

func (apprt *AppRuntimeImpl) addError(err error) {
	apprt.errs = append(apprt.errs, err)
}

func (apprt *AppRuntimeImpl) Err() error {
	return dashutil.ConvertErrArray(apprt.errs)
}

func (linkrt *LinkRuntimeImpl) addError(err error) {
	linkrt.errs = append(linkrt.errs, err)
}

func (linkrt *LinkRuntimeImpl) Err() error {
	return dashutil.ConvertErrArray(linkrt.errs)
}
