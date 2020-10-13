package dashutil

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ControlLocator struct {
	LocId     string
	ControlId string
	ControlTs int64
}

const INVALID_CLOC = "INVALID"

var LocIdRe = regexp.MustCompile("^((/eph-ctx/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36})|(/panel/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)|(/eph-sc/[a-f0-9-]{36}))$")

func (cl ControlLocator) String() string {
	if cl.ControlId == "" || cl.LocId == "" || cl.LocId == INVALID_CLOC {
		return INVALID_CLOC
	}
	return cl.LocId + "|" + cl.ControlId
}

func (cl ControlLocator) IsValid() bool {
	return cl.LocId != INVALID_CLOC && cl.LocId != ""
}

func InvalidCloc() ControlLocator {
	return ControlLocator{LocId: INVALID_CLOC}
}

func MakeControlLoc(locId string, controlId string) ControlLocator {
	rtn, err := MakeControlLocErr(locId, controlId)
	if err != nil {
		panic(err)
	}
	return rtn
}

func MakeControlLocTs(locId string, controlId string, controlTs int64) ControlLocator {
	rtn, err := MakeControlLocErr(locId, controlId)
	if err != nil {
		panic(err)
	}
	rtn.ControlTs = controlTs
	return rtn
}

func MakeControlLocErr(locId string, controlId string) (ControlLocator, error) {
	if !IsUUIDValid(controlId) {
		return ControlLocator{}, errors.New("Invalid controlId passed to MakeControlLoc")
	}
	if !LocIdRe.MatchString(locId) {
		return ControlLocator{}, errors.New("Invalid locId passed to MakeControlLoc")
	}
	return ControlLocator{LocId: locId, ControlId: controlId}, nil
}

func MakeZPLocId(zoneName string, panelName string) string {
	if !IsZoneNameValid(zoneName) || !IsPanelNameValid(panelName) {
		panic("Invalid zonename/panelname passed to MakeZPLocId")
	}
	locId := "/panel/" + zoneName + "/" + panelName
	return locId
}

func MakeZPControlLoc(zoneName string, panelName string, controlId string) ControlLocator {
	if !IsUUIDValid(controlId) {
		panic("Invalid controlId passed to MakeZPControlLoc")
	}
	locId := MakeZPLocId(zoneName, panelName)
	return MakeControlLoc(locId, controlId)
}

func MakeEphCtxLocId(feClientId string, ctxId string, reqId string) string {
	if !IsUUIDValid(feClientId) || !IsUUIDValid(ctxId) || !IsUUIDValid(reqId) {
		panic("Invalid FeClientId/CtxId/ReqId passed to MakeEphCtxLocId")
	}
	return "/eph-ctx/" + feClientId + "/" + ctxId + "/" + reqId
}

func MakeEphScLocId(parentControlId string) string {
	if !IsUUIDValid(parentControlId) {
		panic("Invalid ParentControlId passed to MakeEphScLocId")
	}
	return "/eph-sc/" + parentControlId
}

func MakeEphCtxControlLoc(feClientId string, ctxId string, reqId string, controlId string) ControlLocator {
	if !IsUUIDValid(controlId) {
		panic("Invalid ControlId passed to MakeEphCtxControlLoc")
	}
	locId := MakeEphCtxLocId(feClientId, ctxId, reqId)
	return MakeControlLoc(locId, controlId)
}

func MakeEphScControlLoc(parentControlId string, controlId string) ControlLocator {
	if !IsUUIDValid(controlId) {
		panic("Invalid ControlId passed to MakeEphCtxControlLoc")
	}
	locId := MakeEphScLocId(parentControlId)
	return MakeControlLoc(locId, controlId)
}

func MustParseControlLocator(s string) ControlLocator {
	cloc, err := ParseControlLocator(s)
	if err != nil {
		panic(err)
	}
	return cloc
}

func ParseControlLocator(s string) (ControlLocator, error) {
	if s == "" {
		return ControlLocator{}, fmt.Errorf("ParseControlLocator empty string")
	}
	if s == INVALID_CLOC {
		return ControlLocator{LocId: INVALID_CLOC}, nil
	}
	parts := strings.Split(s, "|")
	if len(parts) != 2 && len(parts) != 3 {
		return ControlLocator{}, fmt.Errorf("ParseControlLocator Invalid ControlLocator str:%s", s)
	}
	cloc, err := MakeControlLocErr(parts[0], parts[1])
	if err != nil {
		return ControlLocator{}, err
	}
	if len(parts) == 3 {
		ts, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return ControlLocator{}, err
		}
		cloc.ControlTs = ts
	}
	return cloc, err
}
