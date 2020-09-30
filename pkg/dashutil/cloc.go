package dashutil

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type ControlLocator struct {
	LocId     string
	ControlId string
}

var UUID_RE = regexp.MustCompile("^[a-fA-F0-9-]{36}$")
var LocIdRe = regexp.MustCompile("^((/eph-ctx/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36})|(/panel/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)|(/eph-sc/[a-f0-9-]{36}))$")

func IsUUIDValid(uuid string) bool {
	if len(uuid) != 36 {
		return false
	}
	return UUID_RE.MatchString(uuid)
}

func (cl ControlLocator) String() string {
	if cl.ControlId == "" || cl.LocId == "" {
		return "invalid"
	}
	return cl.LocId + "|" + cl.ControlId
}

func MakeControlLoc(locId string, controlId string) ControlLocator {
	rtn, err := MakeControlLocErr(locId, controlId)
	if err != nil {
		panic(err)
	}
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
	locId := "/panel/" + zoneName + "/" + panelName
	if !LocIdRe.MatchString(locId) {
		panic("Invalid zonename/panelname passed to MakeZPLocId")
	}
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
	parts := strings.Split(s, "|")
	if len(parts) != 2 {
		return ControlLocator{}, fmt.Errorf("ParseControlLocator Invalid ControlLocator str:%s", s)
	}
	cloc, err := MakeControlLocErr(parts[0], parts[1])
	return cloc, err
}
