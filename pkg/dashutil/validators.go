package dashutil

import "regexp"

const (
	ZONENAME_MAX    = 20
	CONTROLNAME_MAX = 30
	PANELNAME_MAX   = 20
	PROCNAME_MAX    = 20
)

var (
	ZONENAME_RE       = regexp.MustCompile("^[a-zA-Z0-9_.-]+$")
	CONTROLNAME_RE    = regexp.MustCompile("^[a-zA-Z0-9_.:#/-]+$")
	PANELNAME_RE      = regexp.MustCompile("^[a-zA-Z0-9_.-]+$")
	PROCNAME_RE       = regexp.MustCompile("^[a-zA-Z0-9_.]+$")
	UUID_RE           = regexp.MustCompile("^[a-fA-F0-9-]{36}$")
	HANDLER_RE        = regexp.MustCompile("^/[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+$")
	BASE64_RE         = regexp.MustCompile("^[a-zA-Z0-9/+=]+$")
	HEX_RE            = regexp.MustCompile("^[a-f0-9]+$")
	IMAGE_MIMETYPE_RE = regexp.MustCompile("^image/[a-z0-9-]+")
)

func IsZoneNameValid(zoneName string) bool {
	if len(zoneName) > ZONENAME_MAX {
		return false
	}
	return ZONENAME_RE.MatchString(zoneName)
}

func IsPanelNameValid(panelName string) bool {
	if len(panelName) > PANELNAME_MAX {
		return false
	}
	return PANELNAME_RE.MatchString(panelName)
}

func IsControlNameValid(controlName string) bool {
	if len(controlName) > CONTROLNAME_MAX {
		return false
	}
	return CONTROLNAME_RE.MatchString(controlName)
}

func IsProcNameValid(procName string) bool {
	if len(procName) > PROCNAME_MAX {
		return false
	}
	return PROCNAME_RE.MatchString(procName)
}

func IsUUIDValid(uuid string) bool {
	if len(uuid) != 36 {
		return false
	}
	return UUID_RE.MatchString(uuid)
}

func IsHandlerPathValid(handler string) bool {
	if len(handler) > 100 {
		return false
	}
	return HANDLER_RE.MatchString(handler)
}

func IsPublicKeyValid(publicKey string) bool {
	if len(publicKey) < 20 || len(publicKey) > 1000 {
		return false
	}
	return BASE64_RE.MatchString(publicKey)
}

func IsSha256HashValid(s string) bool {
	if len(s) != 64 {
		return false
	}
	return HEX_RE.MatchString(s)
}

func IsImageMimeTypeValid(s string) bool {
	if len(s) == 0 || len(s) > 50 {
		return false
	}
	return IMAGE_MIMETYPE_RE.MatchString(s)
}
