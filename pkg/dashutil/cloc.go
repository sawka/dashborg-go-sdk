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

const (
	panelLocId  = "/panel/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+"
	scLocId     = "/sc/[a-f0-9-]{36}"
	ephCtxLocId = "/eph-ctx/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36}"
	ephScLocId  = "/eph-sc/[a-f0-9-]{36}"
)

var LocIdRe = regexp.MustCompile("^((" + panelLocId + ")|(" + scLocId + ")|(" + ephCtxLocId + ")|(" + ephScLocId + "))$")

func (cl ControlLocator) String() string {
	if cl.ControlId == "" || cl.LocId == "" || cl.LocId == INVALID_CLOC {
		return INVALID_CLOC
	}
	if cl.ControlTs != 0 {
		return cl.LocId + "|" + cl.ControlId + "|" + strconv.FormatInt(cl.ControlTs, 10)
	}
	return cl.LocId + "|" + cl.ControlId
}

func (cl ControlLocator) IsValid() bool {
	return cl.LocId != INVALID_CLOC && cl.LocId != ""
}

func (cl ControlLocator) IsEph() bool {
	return strings.HasPrefix(cl.LocId, "/eph")
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
	if controlTs == 0 {
		panic("Invalid ControlTs, cannot be 0")
	}
	if !strings.HasPrefix(rtn.LocId, "/sc/") && !strings.HasPrefix(rtn.LocId, "/eph-sc/") {
		panic("Can only add ControlTs to /sc/ and /eph-sc/ Control Locators")
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
	if !IsUUIDValid(feClientId) {
		panic(fmt.Sprintf("Invalid FeClientId MakeEphCtxLocId feclientid:%s", feClientId))
	}
	if !IsUUIDValid(ctxId) {
		panic(fmt.Sprintf("Invalid CtxId ctxid:%s", ctxId))
	}
	if !IsUUIDValid(reqId) {
		panic(fmt.Sprintf("Invalid ReqId passed to MakeEphCtxLocId reqid:%s", reqId))
	}
	return "/eph-ctx/" + feClientId + "/" + ctxId + "/" + reqId
}

func MakeScLocId(isEph bool, parentControlId string) string {
	if !IsUUIDValid(parentControlId) {
		panic("Invalid ParentControlId passed to MakeEphScLocId")
	}
	if isEph {
		return "/eph-sc/" + parentControlId
	} else {
		return "/sc/" + parentControlId
	}
}

func MakeEphCtxControlLoc(feClientId string, ctxId string, reqId string, controlId string) ControlLocator {
	if !IsUUIDValid(controlId) {
		panic("Invalid ControlId passed to MakeEphCtxControlLoc")
	}
	locId := MakeEphCtxLocId(feClientId, ctxId, reqId)
	return MakeControlLoc(locId, controlId)
}

func MakeScControlLoc(isEph bool, parentControlId string, controlId string) ControlLocator {
	if !IsUUIDValid(controlId) {
		panic("Invalid ControlId passed to MakeEphCtxControlLoc")
	}
	locId := MakeScLocId(isEph, parentControlId)
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
	if (strings.HasPrefix(cloc.LocId, "/sc/") || strings.HasPrefix(cloc.LocId, "/eph-sc/")) && cloc.ControlTs == 0 {
		return ControlLocator{}, fmt.Errorf("sc controllocators must have a ControlTs")
	}
	return cloc, err
}
