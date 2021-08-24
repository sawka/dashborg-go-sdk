package dash

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

var errType = reflect.TypeOf((*error)(nil)).Elem()
var interfaceType = reflect.TypeOf((*interface{})(nil)).Elem()
var reqType = reflect.TypeOf(&Request{})

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
func (apprt *AppRuntimeImpl) makeCallArgs(hType reflect.Type, req *Request) ([]reflect.Value, error) {
	rtn := make([]reflect.Value, hType.NumIn())
	if hType.NumIn() == 0 {
		return rtn, nil
	}
	argNum := 0
	if hType.In(argNum) == reqType {
		rtn[argNum] = reflect.ValueOf(req)
		argNum++
	}
	if argNum == hType.NumIn() {
		return rtn, nil
	}
	stateType := apprt.appStateType
	if stateType != nil && stateType == hType.In(argNum) {
		stateV, err := unmarshalToType(req.rawData.AppStateJson, stateType)
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
		err := ca_unmarshalMulti(hType, rtn, argNum, req.rawData.DataJson)
		if err != nil {
			return nil, err
		}
	} else {
		err := ca_unmarshalSingle(hType, rtn, argNum, req.rawData.DataJson)
		if err != nil {
			return nil, err
		}
	}
	return rtn, nil
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

type CallHandlerOpts struct {
	StateType reflect.Type
}

// HandlerEx registers a handler using reflection.  The function must
// return (interface{}, error) or error.  First optional argument to the function is a *dash.Request.  2nd optional
// argument is the AppStateType (if one has been set in the app).  The rest of the arguments
// are mapped to the request Data as an array.  If request Data is longer, the arguments are ignored.  If
// request Data is shorter, the missing arguments are set to their zero value.  If request Data is not
// an array, it will be converted to a single element array, if request Data is null it will be converted
// to a zero-element array.  The handler will throw an error if the Data or AppState values cannot be
// converted to their respective go types (using json.Unmarshal).
func (apprt *AppRuntimeImpl) Handler(path string, handlerFn interface{}) error {
	if !dashutil.IsHandlerPathValid(path) {
		return fmt.Errorf("Invalid handler path")
	}
	hType := reflect.TypeOf(handlerFn)
	if hType.Kind() != reflect.Func {
		return fmt.Errorf("handlerFn must be a func")
	}
	if !checkOutput(hType, errType) && !checkOutput(hType, interfaceType, errType) {
		return fmt.Errorf("Dashborg Panel Handler must return (interface{}, error) or error")
	}
	hVal := reflect.ValueOf(handlerFn)
	hfn := func(req *Request) (interface{}, error) {
		args, err := apprt.makeCallArgs(hType, req)
		if err != nil {
			return nil, err
		}
		rtnVals := hVal.Call(args)
		if len(rtnVals) == 2 {
			// (interface{}, error)
			var errRtn error
			if !rtnVals[1].IsNil() {
				errRtn = rtnVals[1].Interface().(error)
			}
			return rtnVals[0].Interface(), errRtn
		} else {
			// error
			if rtnVals[0].IsNil() {
				return nil, nil
			}
			return nil, rtnVals[0].Interface().(error)
		}
	}
	apprt.setHandler(path, handlerType{HandlerFn: hfn})
	return nil
}
