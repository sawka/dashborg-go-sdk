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
	pathFragDefault  = "@default"
	pathFragInit     = "@init"
	pathFragHtml     = "@html"
	pathFragTypeInfo = "@typeinfo"
	pathFragDyn      = "@dyn"
	pathFragPageInit = "@pageinit"
)

type handlerType struct {
	HandlerFn   func(req *AppRequest) (interface{}, error)
	HandlerInfo *runtimeHandlerInfo
	Opts        HandlerOpts
}

// type = any, bool, int, float, string, map, array, struct, blob
type runtimeTypeInfo struct {
	Type       string             `json:"type"`
	Strict     bool               `json:"strict"`
	Name       string             `json:"name,omitempty"`
	MimeType   string             `json:"mimetype,omitempty"`
	ElemType   *runtimeTypeInfo   `json:"elemtype,omitempty"`
	FieldTypes []*runtimeTypeInfo `json:"structtype,omitempty"`
}

type runtimeHandlerInfo struct {
	Name           string            `json:"name"`
	Display        string            `json:"display,omitempty"`
	FormDisplay    string            `json:"formdisplay,omitempty"`
	ResultsDisplay string            `json:"resultsdisplay,omitempty"`
	Description    string            `json:"description,omitempty"`
	Hidden         bool              `json:"hidden,omitempty"`
	Pure           bool              `json:"pure,omitempty"`
	AutoCall       bool              `json:"autocall,omitempty"`
	ContextParam   bool              `json:"contextparam,omitempty"`
	ReqParam       bool              `json:"reqparam,omitempty"`
	AppStateParam  bool              `json:"appstateparam,omitempty"`
	RtnType        *runtimeTypeInfo  `json:"rtntype"`
	ParamsType     []runtimeTypeInfo `json:"paramstype"`
}

type LinkRuntime interface {
	RunHandler(req *AppRequest) (interface{}, error)
}

type HandlerOpts struct {
	Hidden         bool
	PureHandler    bool
	Display        string
	FormDisplay    string
	ResultsDisplay string
}

type LinkRuntimeImpl struct {
	lock        *sync.Mutex
	middlewares []middlewareType
	handlers    map[string]handlerType
	errs        []error
}

type handlerFuncType = func(req *AppRequest) (interface{}, error)

type AppRuntimeImpl struct {
	lock         *sync.Mutex
	appStateType reflect.Type
	handlers     map[string]handlerType
	pageHandlers map[string]handlerFuncType
	middlewares  []middlewareType
	errs         []error
}

type runtimeImplIf interface {
	addError(err error)
	setHandler(path string, handler handlerType)
	getStateType() reflect.Type
}

type HasErr interface {
	Err() error
}

func MakeAppRuntime() *AppRuntimeImpl {
	rtn := &AppRuntimeImpl{
		lock:         &sync.Mutex{},
		handlers:     make(map[string]handlerType),
		pageHandlers: make(map[string]handlerFuncType),
	}
	rtn.SetInitHandler(func() {}, &HandlerOpts{Hidden: true})
	rtn.Handler(pathFragPageInit, rtn.pageInitHandler, &HandlerOpts{Hidden: true})
	rtn.PureHandler(pathFragTypeInfo, rtn.GetHandlerInfo, &HandlerOpts{Hidden: true})
	return rtn
}

func (apprt *AppRuntimeImpl) setHandler(path string, handler handlerType) {
	apprt.lock.Lock()
	defer apprt.lock.Unlock()
	apprt.handlers[path] = handler
}

func (apprt *AppRuntimeImpl) getStateType() reflect.Type {
	return apprt.appStateType
}

func (apprt *AppRuntimeImpl) GetHandlerInfo() (interface{}, error) {
	apprt.lock.Lock()
	defer apprt.lock.Unlock()
	var rtn []*runtimeHandlerInfo
	for _, hval := range apprt.handlers {
		if hval.HandlerInfo.Hidden {
			continue
		}
		rtn = append(rtn, hval.HandlerInfo)
	}
	sort.Slice(rtn, func(i int, j int) bool {
		return rtn[i].Name < rtn[j].Name
	})
	return rtn, nil
}

func (apprt *AppRuntimeImpl) pageInitHandler(req *AppRequest, pageName string) (interface{}, error) {
	handlerFn := apprt.pageHandlers[pageName]
	if handlerFn == nil {
		return nil, nil
	}
	return handlerFn(req)
}

func (apprt *AppRuntimeImpl) SetPageHandler(pageName string, handlerFn interface{}) {
	hfn, err := convertHandlerFn(apprt, handlerFn, true, HandlerOpts{})
	if err != nil {
		apprt.addError(fmt.Errorf("Error in SetPageHandler(%s): %v", pageName, err))
		return
	}
	apprt.pageHandlers[pageName] = hfn
}

func (apprt *AppRuntimeImpl) RunHandler(req *AppRequest) (interface{}, error) {
	_, _, pathFrag, err := dashutil.ParseFullPath(req.info.Path, true)
	if err != nil {
		return nil, dasherr.ValidateErr(fmt.Errorf("Invalid Path: %w", err))
	}
	if pathFrag == "" {
		pathFrag = pathFragDefault
	}
	apprt.lock.Lock()
	hval, ok := apprt.handlers[pathFrag]
	mws := apprt.middlewares
	apprt.lock.Unlock()
	if !ok {
		return nil, dasherr.ErrWithCode(dasherr.ErrCodeNoHandler, fmt.Errorf("No handler found for %s", dashutil.SimplifyPath(req.RequestInfo().Path, nil)))
	}
	if req.info.RequestMethod == RequestMethodGet && !hval.Opts.PureHandler {
		return nil, dasherr.ValidateErr(fmt.Errorf("GET/data request to non-pure handler '%s'", pathFrag))
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
	if appStateType != nil {
		isStruct := appStateType.Kind() == reflect.Struct
		isStructPtr := appStateType.Kind() == reflect.Ptr && appStateType.Elem().Kind() == reflect.Struct
		if !isStruct && !isStructPtr {
			apprt.addError(fmt.Errorf("AppStateType must be a struct or pointer to struct"))
		}
	}
	apprt.appStateType = appStateType
}

func addMiddlewares(mws []middlewareType, mw middlewareType) []middlewareType {
	newmws := make([]middlewareType, len(mws)+1)
	copy(newmws, mws)
	newmws[len(mws)] = mw
	sort.Slice(newmws, func(i int, j int) bool {
		mw1 := newmws[i]
		mw2 := newmws[j]
		return mw1.Priority > mw2.Priority
	})
	return newmws
}

func removeMiddleware(mws []middlewareType, name string) []middlewareType {
	newmws := make([]middlewareType, 0)
	for _, mw := range mws {
		if mw.Name == name {
			continue
		}
		newmws = append(newmws, mw)
	}
	return newmws
}

func (apprt *AppRuntimeImpl) AddRawMiddleware(name string, mwFunc MiddlewareFuncType, priority float64) {
	apprt.RemoveMiddleware(name)
	apprt.lock.Lock()
	defer apprt.lock.Unlock()
	newmw := middlewareType{Name: name, Fn: mwFunc, Priority: priority}
	apprt.middlewares = addMiddlewares(apprt.middlewares, newmw)
}

func (apprt *AppRuntimeImpl) RemoveMiddleware(name string) {
	apprt.lock.Lock()
	defer apprt.lock.Unlock()
	apprt.middlewares = removeMiddleware(apprt.middlewares, name)
}

func (apprt *AppRuntimeImpl) SetRawHandler(handlerName string, handlerFn func(req *AppRequest) (interface{}, error), opts *HandlerOpts) error {
	if opts == nil {
		opts = &HandlerOpts{}
	}
	if !dashutil.IsPathFragValid(handlerName) {
		return fmt.Errorf("Invalid handler name")
	}
	hinfo, err := makeHandlerInfo(apprt, handlerName, handlerFn, *opts)
	if err != nil {
		return err
	}
	apprt.setHandler(handlerName, handlerType{HandlerFn: handlerFn, Opts: *opts, HandlerInfo: hinfo})
	return nil
}

func (apprt *AppRuntimeImpl) SetInitHandler(handlerFn interface{}, opts ...*HandlerOpts) {
	apprt.Handler(pathFragInit, handlerFn, opts...)
}

func (apprt *AppRuntimeImpl) SetHtmlHandler(handlerFn interface{}, opts ...*HandlerOpts) {
	apprt.Handler(pathFragHtml, handlerFn, opts...)
}

func MakeRuntime() *LinkRuntimeImpl {
	rtn := &LinkRuntimeImpl{
		lock:     &sync.Mutex{},
		handlers: make(map[string]handlerType),
	}
	rtn.PureHandler(pathFragTypeInfo, rtn.GetHandlerInfo)
	return rtn
}

func MakeSingleFnRuntime(handlerFn interface{}) *LinkRuntimeImpl {
	rtn := &LinkRuntimeImpl{
		lock:     &sync.Mutex{},
		handlers: make(map[string]handlerType),
	}
	rtn.Handler(pathFragDefault, handlerFn, nil)
	return rtn
}

func (linkrt *LinkRuntimeImpl) GetHandlerInfo() (interface{}, error) {
	linkrt.lock.Lock()
	defer linkrt.lock.Unlock()
	var rtn []*runtimeHandlerInfo
	for _, hval := range linkrt.handlers {
		if hval.HandlerInfo.Hidden {
			continue
		}
		rtn = append(rtn, hval.HandlerInfo)
	}
	sort.Slice(rtn, func(i int, j int) bool {
		return rtn[i].Name < rtn[j].Name
	})
	return rtn, nil
}

func (linkrt *LinkRuntimeImpl) setHandler(name string, fn handlerType) {
	linkrt.lock.Lock()
	defer linkrt.lock.Unlock()
	linkrt.handlers[name] = fn
}

func (linkrt *LinkRuntimeImpl) RunHandler(req *AppRequest) (interface{}, error) {
	info := req.RequestInfo()
	if info.RequestType != requestTypePath {
		return nil, dasherr.ValidateErr(fmt.Errorf("Invalid RequestType for linked runtime"))
	}
	_, _, pathFrag, err := dashutil.ParseFullPath(req.info.Path, true)
	if err != nil {
		return nil, dasherr.ValidateErr(fmt.Errorf("Invalid Path: %w", err))
	}
	if pathFrag == "" {
		pathFrag = pathFragDefault
	}
	linkrt.lock.Lock()
	hval, ok := linkrt.handlers[pathFrag]
	mws := linkrt.middlewares
	linkrt.lock.Unlock()
	if !ok {
		return nil, dasherr.ErrWithCode(dasherr.ErrCodeNoHandler, fmt.Errorf("No handler found for %s", dashutil.SimplifyPath(info.Path, nil)))
	}
	if req.info.RequestMethod == RequestMethodGet && !hval.Opts.PureHandler {
		return nil, dasherr.ValidateErr(fmt.Errorf("GET/Data request to non-pure handler"))
	}
	rtn, err := mwHelper(req, hval, mws, 0)
	if err != nil {
		return nil, err
	}
	return rtn, nil
}

func (linkrt *LinkRuntimeImpl) SetRawHandler(handlerName string, handlerFn func(req Request) (interface{}, error), opts *HandlerOpts) error {
	if opts == nil {
		opts = &HandlerOpts{}
	}
	if !dashutil.IsPathFragValid(handlerName) {
		return fmt.Errorf("Invalid handler name")
	}
	whfn := func(req *AppRequest) (interface{}, error) {
		return handlerFn(req)
	}
	linkrt.setHandler(handlerName, handlerType{HandlerFn: whfn, Opts: *opts})
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

func (linkrt *LinkRuntimeImpl) AddRawMiddleware(name string, mwFunc MiddlewareFuncType, priority float64) {
	linkrt.RemoveMiddleware(name)
	linkrt.lock.Lock()
	defer linkrt.lock.Unlock()
	newmw := middlewareType{Name: name, Fn: mwFunc, Priority: priority}
	linkrt.middlewares = addMiddlewares(linkrt.middlewares, newmw)
}

func (linkrt *LinkRuntimeImpl) RemoveMiddleware(name string) {
	linkrt.lock.Lock()
	defer linkrt.lock.Unlock()
	linkrt.middlewares = removeMiddleware(linkrt.middlewares, name)
}

func (linkrt *LinkRuntimeImpl) getStateType() reflect.Type {
	return nil
}
