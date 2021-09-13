package dash

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

var errType = reflect.TypeOf((*error)(nil)).Elem()
var interfaceType = reflect.TypeOf((*interface{})(nil)).Elem()
var appReqType = reflect.TypeOf(&AppRequest{})
var reqType = reflect.TypeOf((*Request)(nil)).Elem()

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
// * *Request
// * AppStateType (if specified in App)
// * data-array args
func makeCallArgs(hType reflect.Type, req Request, appReq bool, pureHandler bool, stateType reflect.Type) ([]reflect.Value, error) {
	rtn := make([]reflect.Value, hType.NumIn())
	if hType.NumIn() == 0 {
		return rtn, nil
	}
	argNum := 0
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
func (apprt *AppRuntimeImpl) Handler(name string, handlerFn interface{}) {
	err := handlerInternal(apprt, name, handlerFn, true, HandlerOpts{})
	if err != nil {
		apprt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func (apprt *AppRuntimeImpl) PureHandler(name string, handlerFn interface{}) {
	err := handlerInternal(apprt, name, handlerFn, true, HandlerOpts{PureHandler: true})
	if err != nil {
		apprt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func (linkrt *LinkRuntimeImpl) Handler(name string, handlerFn interface{}) {
	err := handlerInternal(linkrt, name, handlerFn, false, HandlerOpts{})
	if err != nil {
		linkrt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func (linkrt *LinkRuntimeImpl) PureHandler(name string, handlerFn interface{}) {
	err := handlerInternal(linkrt, name, handlerFn, false, HandlerOpts{PureHandler: true})
	if err != nil {
		linkrt.addError(fmt.Errorf("Error adding handler '%s': %w", name, err))
	}
}

func handlerInternal(rti runtimeImplIf, name string, handlerFn interface{}, isAppRuntime bool, opts HandlerOpts) error {
	if !dashutil.IsPathFragValid(name) {
		return fmt.Errorf("Invalid handler name")
	}
	hType := reflect.TypeOf(handlerFn)
	err := validateHandler(hType, isAppRuntime, opts.PureHandler, rti.getStateType())
	if err != nil {
		return err
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
	rti.setHandler(name, handlerType{HandlerFn: hfn, Opts: opts})
	return nil
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
