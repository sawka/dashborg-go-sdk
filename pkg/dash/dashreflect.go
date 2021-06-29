package dash

import (
	"encoding/json"
	"fmt"
	"reflect"
)

var errType = reflect.TypeOf((*error)(nil)).Elem()
var interfaceType = reflect.TypeOf((*interface{})(nil)).Elem()
var panelReqType = reflect.TypeOf(&Request{})

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

func makeCallArgs(hType reflect.Type, req *Request) ([]reflect.Value, error) {
	rtn := make([]reflect.Value, hType.NumIn())
	rtn[0] = reflect.ValueOf(req)
	if len(rtn) >= 2 {
		// state-type
		stateV, err := unmarshalToType(req.appStateJson, hType.In(1))
		if err != nil {
			return nil, fmt.Errorf("Cannot unmarshal appStateJson to type:%v err:%v", hType.In(1), err)
		}
		rtn[1] = stateV
	}
	if len(rtn) >= 3 {
		dataV, err := unmarshalToType(req.dataJson, hType.In(2))
		if err != nil {
			return nil, fmt.Errorf("Cannot unmarshal dataJson to type:%v err:%v", hType.In(2), err)
		}
		rtn[2] = dataV
	}
	return rtn, nil
}

func ca2_unmarshalNil(hType reflect.Type, args []reflect.Value, argNum int) {
	for argNum < hType.NumIn() {
		args[argNum] = reflect.Zero(hType.In(argNum))
		argNum++
	}
}

func ca2_unmarshalSingle(hType reflect.Type, args []reflect.Value, argNum int, jsonStr string) error {
	if argNum >= hType.NumIn() {
		return nil
	}
	argV, err := unmarshalToType(jsonStr, hType.In(argNum))
	if err != nil {
		return err
	}
	args[argNum] = argV
	argNum++
	ca2_unmarshalNil(hType, args, argNum)
	return nil
}

func ca2_unmarshalMulti(hType reflect.Type, args []reflect.Value, argNum int, jsonStr string) error {
	if argNum >= hType.NumIn() {
		return nil
	}
	ca2_unmarshalNil(hType, args, argNum)
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
		args[i+argNum] = reflect.ValueOf(outVals[i]).Elem()
	}
	argNum += len(outVals)
	ca2_unmarshalNil(hType, args, argNum)
	return nil
}

func makeCallArgs2(hType reflect.Type, opts CallHandlerOpts, req *Request) ([]reflect.Value, error) {
	rtn := make([]reflect.Value, hType.NumIn())
	if hType.NumIn() == 0 {
		return rtn, nil
	}
	argNum := 0
	if hType.In(argNum) == panelReqType {
		rtn[argNum] = reflect.ValueOf(req)
		argNum++
	}
	if argNum == hType.NumIn() {
		return rtn, nil
	}
	if opts.StateType != nil {
		stateV, err := unmarshalToType(req.appStateJson, opts.StateType)
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
		ca2_unmarshalNil(hType, rtn, argNum)
	} else if reflect.ValueOf(dataInterface).Kind() == reflect.Slice {
		err := ca2_unmarshalMulti(hType, rtn, argNum, req.dataJson)
		if err != nil {
			return nil, err
		}
	} else {
		err := ca2_unmarshalSingle(hType, rtn, argNum, req.dataJson)
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

// func RegisterCallHandlerEx(panelName string, path string, handlerFn interface{}, opts *CallHandlerOpts) error {
// 	if opts == nil {
// 		opts = &CallHandlerOpts{}
// 	}
// 	hType := reflect.TypeOf(handlerFn)
// 	if !checkOutput(hType, interfaceType, errType) {
// 		return fmt.Errorf("Dashborg Call Handler must return two values (interface{}, error)")
// 	}
// 	if opts.StateType != nil {
// 		if hType.NumIn() == 0 || !opts.StateType.AssignableTo(hType.In(0)) {
// 			return fmt.Errorf("Dashborg Call Handler with StateType option must take StateType as first parameter")
// 		}
// 	}
// 	hVal := reflect.ValueOf(handlerFn)
// 	RegisterDataHandler(panelName, path, func(req *Request) (interface{}, error) {
// 		args, err := makeCallArgs2(hType, *opts, req)
// 		if err != nil {
// 			return nil, err
// 		}
// 		rtnVals := hVal.Call(args)
// 		if rtnVals[1].IsNil() {
// 			return rtnVals[0].Interface(), nil
// 		}
// 		return rtnVals[0].Interface(), rtnVals[1].Interface().(error)
// 	})
// 	return nil
// }

// RegisterAppHandlerEx registers a panel handler using reflection. The handler function
// must return exactly one error value.  It must also take between 1-3 arguments.
// The first parameter must be a Request.  The second (optional)
// parameter is appState, and the third (optional) parameter is Data.  Dashborg will attempt to
// unmarshal the raw JSON of appState and Data to the types in the handler signature using
// the standard Go json.Unmarshal() function.  If an error occurs during unmarshalling it will
// be returned to the Dashborg service (and your handler function will never run).
func (app *App) HandlerEx(path string, handlerFn interface{}) error {
	hType := reflect.TypeOf(handlerFn)
	if !checkOutput(hType, errType) {
		return fmt.Errorf("Dashborg Panel Handler must return one error value")
	}
	paramsOk := (hType.NumIn() >= 1 && hType.NumIn() <= 3) && hType.In(0) == panelReqType
	if !paramsOk {
		return fmt.Errorf("Dashborg Panel Handler must have 1-3 arguments (Request, stateType, dataType)")
	}
	hVal := reflect.ValueOf(handlerFn)
	app.Handler(path, func(req *Request) error {
		args, err := makeCallArgs(hType, req)
		if err != nil {
			return err
		}
		rtnVals := hVal.Call(args)
		if rtnVals[0].IsNil() {
			return nil
		}
		return rtnVals[0].Interface().(error)
	})
	return nil
}

// RegisterPanelDataEx registers a panel handler using reflection.  The handler function must
// return exactly two values (interface{}, error).  It must also take between 1-3 arguments.
// The first parameter must be a Request.  The second (optional)
// parameter is appState, and the third (optional) parameter is Data.  Dashborg will attempt to
// unmarshal the raw JSON of appState and Data to the types in the handler signature using
// the standard Go json.Unmarshal() function.  If an error occurs during unmarshalling it will
// be returned to the Dashborg service (and your handler function will never run).
func (app *App) DataHandlerEx(path string, handlerFn interface{}) error {
	hType := reflect.TypeOf(handlerFn)
	if !checkOutput(hType, interfaceType, errType) {
		return fmt.Errorf("Dashborg Data Handler must return two values (interface{}, error)")
	}
	paramsOk := (hType.NumIn() >= 1 && hType.NumIn() <= 3) && hType.In(0) == panelReqType
	if !paramsOk {
		return fmt.Errorf("Dashborg Data Handler must have 1-3 arguments (Request, stateType, dataType)")
	}
	hVal := reflect.ValueOf(handlerFn)
	app.DataHandler(path, func(req *Request) (interface{}, error) {
		args, err := makeCallArgs(hType, req)
		if err != nil {
			return nil, err
		}
		rtnVals := hVal.Call(args)
		if rtnVals[1].IsNil() {
			return rtnVals[0].Interface(), nil
		}
		return rtnVals[0].Interface(), rtnVals[1].Interface().(error)
	})
	return nil
}
