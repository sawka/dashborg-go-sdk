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

func checkInput(mType reflect.Type, inputTypes ...reflect.Type) bool {
	if mType.NumIn() != len(inputTypes) {
		return false
	}
	for idx, t := range inputTypes {
		if t == nil {
			continue
		}
		if !t.AssignableTo(mType.In(idx)) {
			return false
		}
	}
	return true
}

func checkPanelHandlerMethod(mType reflect.Type, stateType reflect.Type) bool {
	if !checkOutput(mType, errType) {
		return false
	}
	return checkInput(mType, nil, panelReqType) ||
		checkInput(mType, nil, panelReqType, stateType) ||
		checkInput(mType, nil, panelReqType, nil) ||
		checkInput(mType, nil, panelReqType, stateType, nil)
}

func checkDataHandlerMethod(mType reflect.Type, stateType reflect.Type) bool {
	if !checkOutput(mType, interfaceType, errType) {
		return false
	}
	return checkInput(mType, nil, panelReqType) ||
		checkInput(mType, nil, panelReqType, stateType) ||
		checkInput(mType, nil, panelReqType, nil) ||
		checkInput(mType, nil, panelReqType, stateType, nil)
}

func makeCallArgs(mType reflect.Type, req *PanelRequest, model interface{}, stateType reflect.Type) ([]reflect.Value, error) {
	rtn := []reflect.Value{
		reflect.ValueOf(model),
		reflect.ValueOf(req),
	}
	if checkInput(mType, nil, panelReqType) {
		return rtn, nil
	} else if checkInput(mType, nil, panelReqType, stateType) {
		stateV, err := unmarshalToType(req.PanelStateJson, stateType)
		if err != nil {
			return nil, err
		}
		rtn = append(rtn, stateV)
		return rtn, nil
	} else if checkInput(mType, nil, panelReqType, nil) {
		dataV, err := unmarshalToType(req.DataJson, mType.In(2))
		if err != nil {
			return nil, err
		}
		rtn = append(rtn, dataV)
		return rtn, nil
	} else if checkInput(mType, nil, panelReqType, stateType, nil) {
		stateV, err := unmarshalToType(req.PanelStateJson, stateType)
		if err != nil {
			return nil, err
		}
		rtn = append(rtn, stateV)
		dataV, err := unmarshalToType(req.DataJson, mType.In(3))
		if err != nil {
			return nil, err
		}
		rtn = append(rtn, dataV)
		return rtn, nil
	} else {
		return nil, fmt.Errorf("Bad Handler Arguments (reflect)")
	}
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

func registerPanelMethod(panelName string, modelStruct interface{}, m reflect.Method, stateType reflect.Type, nameMapping map[string]string) {
	var path string
	if m.Name == "RootHandler" {
		path = "/"
	} else {
		if newName, ok := nameMapping[m.Name]; ok {
			path = newName
		} else {
			path = "/" + m.Name
		}
	}
	mType := m.Type
	if checkPanelHandlerMethod(mType, stateType) {
		RegisterPanelHandler(panelName, path, func(req *PanelRequest) error {
			args, err := makeCallArgs(mType, req, modelStruct, stateType)
			if err != nil {
				return err
			}
			rtnVals := m.Func.Call(args)
			if rtnVals[0].IsNil() {
				return nil
			}
			return rtnVals[0].Interface().(error)
		})
	} else if checkDataHandlerMethod(mType, stateType) {
		RegisterDataHandler(panelName, path, func(req *PanelRequest) (interface{}, error) {
			args, err := makeCallArgs(mType, req, modelStruct, stateType)
			if err != nil {
				return nil, err
			}
			rtnVals := m.Func.Call(args)
			if rtnVals[1].IsNil() {
				return rtnVals[0].Interface(), nil
			}
			return rtnVals[0].Interface(), rtnVals[1].Interface().(error)
		})
	} else {
		fmt.Printf("skipping method with strange sig %#v\n", m)
	}
}

func RegisterPanelModel(panelName string, modelStruct interface{}, stateStruct interface{}, nameMapping map[string]string) {
	modelType := reflect.ValueOf(modelStruct).Type()
	numMethods := modelType.NumMethod()
	var stateType reflect.Type
	if stateStruct == nil {
		stateType = reflect.TypeOf((*interface{})(nil)).Elem()
	} else {
		stateType = reflect.TypeOf(stateStruct)
	}
	for i := 0; i < numMethods; i++ {
		m := modelType.Method(i)
		if m.Type.NumIn() >= 2 && panelReqType.AssignableTo(m.Type.In(1)) {
			registerPanelMethod(panelName, modelStruct, m, stateType, nameMapping)
		}
	}
}
