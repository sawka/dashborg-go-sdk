package dash

import (
	"encoding/json"
	"fmt"
	"reflect"
)

var errType = reflect.TypeOf((*error)(nil)).Elem()
var interfaceType = reflect.TypeOf((*interface{})(nil)).Elem()
var panelReqType = reflect.TypeOf(&PanelRequest{})

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

func makeCallArgs(hType reflect.Type, req *PanelRequest) ([]reflect.Value, error) {
	rtn := make([]reflect.Value, hType.NumIn())
	rtn[0] = reflect.ValueOf(req)
	if len(rtn) >= 2 {
		// state-type
		stateV, err := unmarshalToType(req.PanelStateJson, hType.In(1))
		if err != nil {
			return nil, fmt.Errorf("Cannot unmarshal PanelStateJson to type:%v err:%v", hType.In(1), err)
		}
		rtn[1] = stateV
	}
	if len(rtn) >= 3 {
		dataV, err := unmarshalToType(req.DataJson, hType.In(2))
		if err != nil {
			return nil, fmt.Errorf("Cannot unmarshal DataJson to type:%v err:%v", hType.In(2), err)
		}
		rtn[2] = dataV
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

// RegisterPanelHandlerEx registers a panel handler using reflection. The handler function
// must return exactly one error value.  It must also take between 1-3 arguments.
// The first parameter must be a *dash.PanelRequest.  The second (optional)
// parameter is PanelState, and the third (optional) parameter is Data.  Dashborg will attempt to
// unmarshal the raw JSON of PanelState and Data to the types in the handler signature using
// the standard Go json.Unmarshal() function.  If an error occurs during unmarshalling it will
// be returned to the Dashborg service (and your handler function will never run).
func RegisterPanelHandlerEx(panelName string, path string, handlerFn interface{}) error {
	hType := reflect.TypeOf(handlerFn)
	if !checkOutput(hType, errType) {
		return fmt.Errorf("Dashborg Panel Handler must return one error value")
	}
	paramsOk := (hType.NumIn() >= 1 && hType.NumIn() <= 3) && hType.In(0) == panelReqType
	if !paramsOk {
		return fmt.Errorf("Dashborg Panel Handler must have 1-3 arguments (*dash.PanelRequest, stateType, dataType)")
	}
	hVal := reflect.ValueOf(handlerFn)
	RegisterPanelHandler(panelName, path, func(req *PanelRequest) error {
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
// The first parameter must be a *dash.PanelRequest.  The second (optional)
// parameter is PanelState, and the third (optional) parameter is Data.  Dashborg will attempt to
// unmarshal the raw JSON of PanelState and Data to the types in the handler signature using
// the standard Go json.Unmarshal() function.  If an error occurs during unmarshalling it will
// be returned to the Dashborg service (and your handler function will never run).
func RegisterDataHandlerEx(panelName string, path string, handlerFn interface{}) error {
	hType := reflect.TypeOf(handlerFn)
	if !checkOutput(hType, interfaceType, errType) {
		return fmt.Errorf("Dashborg Data Handler must return two values (interface{}, error)")
	}
	paramsOk := (hType.NumIn() >= 1 && hType.NumIn() <= 3) && hType.In(0) == panelReqType
	if !paramsOk {
		return fmt.Errorf("Dashborg Panel Handler must have 1-3 arguments (*dash.PanelRequest, stateType, dataType)")
	}
	hVal := reflect.ValueOf(handlerFn)
	RegisterDataHandler(panelName, path, func(req *PanelRequest) (interface{}, error) {
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
