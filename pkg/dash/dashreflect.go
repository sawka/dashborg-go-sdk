package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

var errType = reflect.TypeOf((*error)(nil)).Elem()
var interfaceType = reflect.TypeOf((*interface{})(nil)).Elem()
var appReqType = reflect.TypeOf(&AppRequest{})
var reqType = reflect.TypeOf((*Request)(nil)).Elem()
var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

func checkOutput(mType reflect.Type, outputTypes ...reflect.Type) bool {
	if mType.NumOut() != len(outputTypes) {
		return false
	}
	for idx, t := range outputTypes {
		if !mType.Out(idx).AssignableTo(t) {
			return false
		}
	}
	return true
}

func ca_unmarshalNil(hType reflect.Type, args []reflect.Value, argNum int) {
	for argNum < hType.NumIn() {
		args[argNum] = reflect.Zero(hType.In(argNum))
		argNum++
	}
}

func ca_unmarshalSingle(hType reflect.Type, args []reflect.Value, argNum int, jsonStr string) error {
	if argNum >= hType.NumIn() {
		return nil
	}
	argV, err := unmarshalToType(jsonStr, hType.In(argNum))
	if err != nil {
		return err
	}
	args[argNum] = argV
	argNum++
	ca_unmarshalNil(hType, args, argNum)
	return nil
}

func ca_unmarshalMulti(hType reflect.Type, args []reflect.Value, argNum int, jsonStr string) error {
	if argNum >= hType.NumIn() {
		return nil
	}
	outVals := make([]interface{}, hType.NumIn()-argNum)
	dataArgsNum := hType.NumIn() - argNum
	for i := argNum; i < hType.NumIn(); i++ {
		outVals[i-argNum] = reflect.New(hType.In(i)).Interface()
	}
	err := json.Unmarshal([]byte(jsonStr), &outVals)
	if err != nil {
		return err
	}
	// outvals can be shorter than hType.NumIn() - argNum (if json is short)
	for i := 0; i < len(outVals) && i < dataArgsNum; i++ {
		if outVals[i] == nil {
			args[i+argNum] = reflect.Zero(hType.In(i + argNum))
		} else {
			args[i+argNum] = reflect.ValueOf(outVals[i]).Elem()
		}
	}
	argNum += len(outVals)
	ca_unmarshalNil(hType, args, argNum)
	return nil
}

// params:
// * Context
// * *Request
// * AppStateType (if specified in App)
// * data-array args
func makeCallArgs(hType reflect.Type, req Request, appReq bool, pureHandler bool, stateType reflect.Type) ([]reflect.Value, error) {
	rtn := make([]reflect.Value, hType.NumIn())
	if hType.NumIn() == 0 {
		return rtn, nil
	}
	argNum := 0
	if hType.In(argNum) == contextType {
		rtn[argNum] = reflect.ValueOf(req.Context())
		argNum++
	}
	if argNum == hType.NumIn() {
		return rtn, nil
	}
	if hType.In(argNum) == appReqType {
		if !appReq {
			return nil, fmt.Errorf("LinkRuntime functions must use dash.Request, not *dash.AppRequest")
		}
		if pureHandler {
			return nil, fmt.Errorf("PureHandlers must use dash.Request, not *dash.AppRequest")
		}
		rtn[argNum] = reflect.ValueOf(req)
		argNum++
	} else if hType.In(argNum) == reqType {
		rtn[argNum] = reflect.ValueOf(req)
		argNum++
	}
	if argNum == hType.NumIn() {
		return rtn, nil
	}
	rawData := req.RawData()
	if stateType != nil && stateType == hType.In(argNum) {
		stateV, err := unmarshalToType(rawData.AppStateJson, stateType)
		if err != nil {
			return nil, fmt.Errorf("Cannot unmarshal appStateJson to type:%v err:%v", hType.In(1), err)
		}
		rtn[argNum] = stateV
		argNum++
	}
	if argNum == hType.NumIn() {
		return rtn, nil
	}
	var dataInterface interface{}
	req.BindData(&dataInterface)
	if dataInterface == nil {
		ca_unmarshalNil(hType, rtn, argNum)
	} else if reflect.ValueOf(dataInterface).Kind() == reflect.Slice {
		err := ca_unmarshalMulti(hType, rtn, argNum, rawData.DataJson)
		if err != nil {
			return nil, err
		}
	} else {
		err := ca_unmarshalSingle(hType, rtn, argNum, rawData.DataJson)
		if err != nil {
			return nil, err
		}
	}
	return rtn, nil
}

func validateHandler(hType reflect.Type, appReq bool, pureHandler bool, stateType reflect.Type) error {
	if hType.Kind() != reflect.Func {
		return fmt.Errorf("handlerFn must be a func")
	}
	if hType.NumOut() != 0 && !checkOutput(hType, errType) && !checkOutput(hType, interfaceType) && !checkOutput(hType, interfaceType, errType) {
		return fmt.Errorf("Invalid handlerFn return, must return void, error, interface{}, or (interface{}, error)")
	}
	if hType.NumIn() == 0 {
		return nil
	}

	argNum := 0
	// check optional context
	if hType.In(argNum) == contextType {
		argNum++
	}
	if hType.NumIn() <= argNum {
		return nil
	}
	// check optional first argument: *dash.AppRequest / dash.Request
	if hType.In(argNum) == appReqType {
		if !appReq {
			return fmt.Errorf("LinkRuntime functions must use dash.Request, not *dash.AppRequest")
		}
		if pureHandler {
			return fmt.Errorf("PureHandlers must use dash.Request, not *dash.AppRequest")
		}
		argNum++
	} else if hType.In(argNum) == reqType {
		argNum++
	}
	if hType.NumIn() <= argNum {
		return nil
	}

	// optional state type
	if stateType != nil && hType.In(argNum) == stateType {
		argNum++
	} else if stateType != nil && stateType.Kind() == reflect.Ptr && hType.In(argNum) == stateType.Elem() {
		return fmt.Errorf("StateType is %v (pointer), but argument is not a pointer: %v", stateType, hType.In(argNum))
	} else if stateType != nil && stateType.Kind() == reflect.Struct && hType.In(argNum) == reflect.PtrTo(stateType) {
		return fmt.Errorf("StateType is %v (struct), but argument a pointer: %v", stateType, hType.In(argNum))
	}

	if hType.NumIn() <= argNum {
		return nil
	}

	// some basic static checking of the rest of the arguments
	for ; argNum < hType.NumIn(); argNum++ {
		inType := hType.In(argNum)
		if inType == appReqType {
			return fmt.Errorf("Invalid arg #%d, *dash.AppRequest must be first argument", argNum+1)
		}
		if inType == reqType {
			return fmt.Errorf("Invalid arg #%d, dash.Request must be first argument", argNum+1)
		}
		if stateType != nil && inType == stateType {
			return fmt.Errorf("Invalid arg #%d, state-type %v, must be first argument or second argument after dash.Request", argNum+1, stateType)
		}
		if inType.Kind() == reflect.Func || inType.Kind() == reflect.Chan || inType.Kind() == reflect.UnsafePointer {
			return fmt.Errorf("Invalid arg #%d, cannot marshal into func/chan/unsafe.Pointer", argNum+1)
		}
		if inType.Kind() == reflect.Map {
			if inType.Key().Kind() != reflect.String {
				return fmt.Errorf("Invalid arg #%d (%v) map arguments must have string keys", argNum, inType)
			}
		}
	}

	return nil
}

func unmarshalToType(jsonData string, rtnType reflect.Type) (reflect.Value, error) {
	if jsonData == "" {
		return reflect.Zero(rtnType), nil
	}
	if rtnType.Kind() == reflect.Ptr {
		rtnV := reflect.New(rtnType.Elem())
		err := json.Unmarshal([]byte(jsonData), rtnV.Interface())
		if err != nil {
			return reflect.Value{}, err
		}
		return rtnV, nil
	} else {
		rtnV := reflect.New(rtnType)
		err := json.Unmarshal([]byte(jsonData), rtnV.Interface())
		if err != nil {
			return reflect.Value{}, err
		}
		return rtnV.Elem(), nil
	}
	return reflect.Zero(rtnType), nil
}

// Handler registers a handler using reflection.
// Return value must be return void, interface{}, error, or (interface{}, error).
// First optional argument to the function is a *dash.AppRequest.
// Second optional argument is the AppStateType (if one has been set in the app runtime).
// The rest of the arguments are mapped to the request Data as an array.  If request Data is longer,
// the arguments are ignored.  If request Data is shorter, the missing arguments are set to their zero value.
// If request Data is not an array, it will be converted to a single element array, if request Data is null
// it will be converted to a zero-element array.  The handler will throw an error if the Data or AppState
// values cannot be converted to their respective go types (using json.Unmarshal).
func (apprt *AppRuntimeImpl) Handler(name string, handlerFn interface{}, opts ...*HandlerOpts) {
	singleOpt := getSingleOpt(opts)
	err := handlerInternal(apprt, name, handlerFn, true, singleOpt)
	if err != nil {
		apprt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func (apprt *AppRuntimeImpl) PureHandler(name string, handlerFn interface{}, opts ...*HandlerOpts) {
	singleOpt := getSingleOpt(opts)
	singleOpt.PureHandler = true
	err := handlerInternal(apprt, name, handlerFn, true, singleOpt)
	if err != nil {
		apprt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func (linkrt *LinkRuntimeImpl) Handler(name string, handlerFn interface{}, opts ...*HandlerOpts) {
	singleOpt := getSingleOpt(opts)
	err := handlerInternal(linkrt, name, handlerFn, false, singleOpt)
	if err != nil {
		linkrt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func (linkrt *LinkRuntimeImpl) PureHandler(name string, handlerFn interface{}, opts ...*HandlerOpts) {
	singleOpt := getSingleOpt(opts)
	singleOpt.PureHandler = true
	err := handlerInternal(linkrt, name, handlerFn, false, singleOpt)
	if err != nil {
		linkrt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func getSingleOpt(opts []*HandlerOpts) HandlerOpts {
	if len(opts) == 0 || opts[0] == nil {
		return HandlerOpts{}
	}
	return *opts[0]
}

func handlerInternal(rti runtimeImplIf, name string, handlerFn interface{}, isAppRuntime bool, opts HandlerOpts) error {
	if !dashutil.IsPathFragValid(name) {
		return fmt.Errorf("Invalid handler name")
	}
	hfn, err := convertHandlerFn(rti, handlerFn, isAppRuntime, opts)
	if err != nil {
		return err
	}
	hinfo, err := makeHandlerInfo(rti, name, handlerFn, opts)
	if err != nil {
		return err
	}
	rti.setHandler(name, handlerType{HandlerFn: hfn, Opts: opts, HandlerInfo: hinfo})
	return nil
}

func convertHandlerFn(rti runtimeImplIf, handlerFn interface{}, isAppRuntime bool, opts HandlerOpts) (handlerFuncType, error) {
	hType := reflect.TypeOf(handlerFn)
	err := validateHandler(hType, isAppRuntime, opts.PureHandler, rti.getStateType())
	if err != nil {
		return nil, err
	}
	hVal := reflect.ValueOf(handlerFn)
	hfn := func(req *AppRequest) (interface{}, error) {
		args, err := makeCallArgs(hType, req, isAppRuntime, opts.PureHandler, rti.getStateType())
		if err != nil {
			return nil, err
		}
		rtnVals := hVal.Call(args)
		return convertRtnVals(hType, rtnVals)
	}
	return hfn, nil
}

func isIntType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true

	default:
		return false
	}
}

func makeTypeInfo(t reflect.Type) (*runtimeTypeInfo, error) {
	if isIntType(t) {
		return &runtimeTypeInfo{Type: "int", Strict: true}, nil
	}
	switch t.Kind() {
	case reflect.Float32, reflect.Float64:
		return &runtimeTypeInfo{Type: "float", Strict: true}, nil

	case reflect.String:
		return &runtimeTypeInfo{Type: "string", Strict: true}, nil

	case reflect.Bool:
		return &runtimeTypeInfo{Type: "bool", Strict: true}, nil

	case reflect.Interface:
		return &runtimeTypeInfo{Type: "any", Strict: false}, nil

	case reflect.Ptr:
		return makeTypeInfo(t.Elem())

	case reflect.Array, reflect.Slice:
		elemType, err := makeTypeInfo(t.Elem())
		if err != nil {
			return nil, err
		}
		return &runtimeTypeInfo{Type: "array", Strict: true, ElemType: elemType}, nil

	case reflect.Map:
		elemType, err := makeTypeInfo(t.Elem())
		if err != nil {
			return nil, err
		}
		keyType := t.Key()
		if keyType.Kind() != reflect.String {
			return nil, fmt.Errorf("Invalid map type, key must be type string")
		}
		return &runtimeTypeInfo{Type: "map", Strict: true, ElemType: elemType}, nil

	case reflect.Struct:
		numField := t.NumField()
		var fieldTypes []*runtimeTypeInfo
		for i := 0; i < numField; i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			fieldType, err := makeTypeInfo(field.Type)
			if err != nil {
				return nil, err
			}
			fieldTypes = append(fieldTypes, fieldType)
		}
		return &runtimeTypeInfo{Type: "struct", Strict: true, FieldTypes: fieldTypes}, nil

	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.UnsafePointer, reflect.Func:
		return nil, fmt.Errorf("Invalid Type: %v", t)
	}
	return nil, fmt.Errorf("Invalid Type: %v", t)
}

func makeTypeInfoFromReturn(hType reflect.Type) (*runtimeTypeInfo, error) {
	if hType.NumOut() == 0 {
		return nil, nil
	}
	if hType.NumOut() == 1 {
		if hType.Out(0) == errType {
			return nil, nil
		}
		return makeTypeInfo(hType.Out(0))
	}
	if hType.NumOut() == 2 {
		if hType.Out(1) != errType {
			return nil, fmt.Errorf("Invalid func return type, if a func returns 2 values, second value must be type 'error'")
		}
		return makeTypeInfo(hType.Out(0))
	}
	return nil, fmt.Errorf("Invalid func return type (can only return a maximum of 2 values)")
}

func checkContextArg(hType reflect.Type, argNum *int) bool {
	if *argNum >= hType.NumIn() {
		return false
	}
	argType := hType.In(*argNum)
	if argType == contextType {
		(*argNum)++
		return true
	}
	return false
}

func checkReqArg(hType reflect.Type, argNum *int) bool {
	if *argNum >= hType.NumIn() {
		return false
	}
	argType := hType.In(*argNum)
	if argType == reqType || argType == appReqType {
		(*argNum)++
		return true
	}
	return false
}

func checkAppStateArg(hType reflect.Type, argNum *int, appStateType reflect.Type) bool {
	if *argNum >= hType.NumIn() {
		return false
	}
	argType := hType.In(*argNum)
	if argType == appStateType {
		(*argNum)++
		return true
	}
	return false
}

func makeHandlerInfo(rti runtimeImplIf, name string, handlerFn interface{}, opts HandlerOpts) (*runtimeHandlerInfo, error) {
	rtn := &runtimeHandlerInfo{
		Name:           name,
		Pure:           opts.PureHandler,
		Hidden:         opts.Hidden,
		Display:        opts.Display,
		FormDisplay:    opts.FormDisplay,
		ResultsDisplay: opts.ResultsDisplay,
	}
	var err error
	hType := reflect.TypeOf(handlerFn)
	rtn.RtnType, err = makeTypeInfoFromReturn(hType)
	if err != nil {
		return nil, err
	}
	argNum := 0
	rtn.ContextParam = checkContextArg(hType, &argNum)
	rtn.ReqParam = checkReqArg(hType, &argNum)
	rtn.AppStateParam = checkAppStateArg(hType, &argNum, rti.getStateType())
	for ; argNum < hType.NumIn(); argNum++ {
		typeInfo, err := makeTypeInfo(hType.In(argNum))
		if err != nil {
			return nil, err
		}
		rtn.ParamsType = append(rtn.ParamsType, *typeInfo)
	}
	return rtn, nil
}

func convertRtnVals(hType reflect.Type, rtnVals []reflect.Value) (interface{}, error) {
	if len(rtnVals) == 0 {
		return nil, nil
	} else if len(rtnVals) == 1 {
		if checkOutput(hType, errType) {
			if rtnVals[0].IsNil() {
				return nil, nil
			}
			return nil, rtnVals[0].Interface().(error)
		}
		return rtnVals[0].Interface(), nil
	} else {
		// (interface{}, error)
		var errRtn error
		if !rtnVals[1].IsNil() {
			errRtn = rtnVals[1].Interface().(error)
		}
		return rtnVals[0].Interface(), errRtn
	}
}
