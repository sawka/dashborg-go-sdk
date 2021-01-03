package dashutil

import "regexp"

const (
	ZONENAME_MAX      = 20
	CONTROLNAME_MAX   = 30
	PANELNAME_MAX     = 20
	PROCNAME_MAX      = 20
	FILENAME_MAX      = 80
	EMAIL_MAX         = 80
	PASSWORD_MAX      = 80
	MIMETYPE_MAX      = 80
	SHA256_HEX_LEN    = 64
	SHA256_B64_LEN    = 44
	UUID_LEN          = 36
	HANDLERPATH_MAX   = 100
	DATAPATH_MAX      = 200
	PATH_MAX          = 100
	TAG_MAX           = 50
	CLIENTVERSION_MAX = 20
	PROCTAGVAL_MAX    = 200
	HOSTDATAVAL_MAX   = 100
)

var (
	ZONENAME_RE       = regexp.MustCompile("^[a-zA-Z0-9_.-]+$")
	CONTROLNAME_RE    = regexp.MustCompile("^[a-zA-Z0-9_.:#/-]+$")
	PANELNAME_RE      = regexp.MustCompile("^[a-zA-Z0-9_.-]+$")
	PROCNAME_RE       = regexp.MustCompile("^[a-zA-Z0-9_.-]+$")
	UUID_RE           = regexp.MustCompile("^[a-fA-F0-9-]{36}$")
	HANDLER_RE        = regexp.MustCompile("^/[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+$")
	BASE64_RE         = regexp.MustCompile("^[a-zA-Z0-9/+=]+$")
	HEX_RE            = regexp.MustCompile("^[a-f0-9]+$")
	IMAGE_MIMETYPE_RE = regexp.MustCompile("^image/[a-z0-9.-]+$")
	MIMETYPE_RE       = regexp.MustCompile("^[a-z0-9.-]+/[a-z0-9.-]+$")
	SIMPLEFILENAME_RE = regexp.MustCompile("^[a-zA-Z0-9._-]+$")
	PATH_RE           = regexp.MustCompile("^/[a-zA-Z0-9._/-]*$")
	TAG_RE            = regexp.MustCompile("^[a-zA-Z0-9._:/-]+$")
	CLIENTVERSION_RE  = regexp.MustCompile("^[a-z0-9_]+-\\d+\\.\\d+\\.\\d+$")

	// https://www.w3.org/TR/2016/REC-html51-20161101/sec-forms.html#email-state-typeemali
	EMAIL_RE = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

	PASSWORD_RE = regexp.MustCompile("^[a-zA-Z0-9]+$")
)

var ValidHandlerType = map[string]bool{"data": true, "handler": true, "stream": true, "panel": true}
var ValidActionType = map[string]bool{"setdata": true, "event": true, "invalidate": true, "panel": true, "auth": true}

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

func IsSimpleFileNameValid(fileName string) bool {
	if len(fileName) > FILENAME_MAX {
		return false
	}
	return SIMPLEFILENAME_RE.MatchString(fileName)
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
	if len(uuid) != UUID_LEN {
		return false
	}
	return UUID_RE.MatchString(uuid)
}

func IsHandlerPathValid(handler string) bool {
	if len(handler) > HANDLERPATH_MAX {
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

func IsSha256HexHashValid(s string) bool {
	if len(s) != SHA256_HEX_LEN {
		return false
	}
	return HEX_RE.MatchString(s)
}

func IsSha256Base64HashValid(s string) bool {
	if len(s) != SHA256_B64_LEN {
		return false
	}
	return BASE64_RE.MatchString(s)
}

func IsMimeTypeValid(s string) bool {
	if len(s) == 0 || len(s) > MIMETYPE_MAX {
		return false
	}
	return MIMETYPE_RE.MatchString(s)
}

func IsImageMimeTypeValid(s string) bool {
	if len(s) == 0 || len(s) > MIMETYPE_MAX {
		return false
	}
	return IMAGE_MIMETYPE_RE.MatchString(s)
}

func IsEmailValid(s string) bool {
	if len(s) == 0 || len(s) > EMAIL_MAX {
		return false
	}
	return EMAIL_RE.MatchString(s)
}

func IsPasswordValid(s string) bool {
	if len(s) == 0 || len(s) > PASSWORD_MAX {
		return false
	}
	return true
}

func IsPathValid(s string) bool {
	if len(s) == 0 || len(s) > PATH_MAX {
		return false
	}
	return PATH_RE.MatchString(s)
}

func IsHandlerTypeValid(s string) bool {
	return ValidHandlerType[s]
}

func IsActionTypeValid(s string) bool {
	return ValidActionType[s]
}

func IsTagValid(s string) bool {
	if len(s) == 0 || len(s) > TAG_MAX {
		return false
	}
	return TAG_RE.MatchString(s)
}

func IsClientVersionValid(s string) bool {
	if len(s) == 0 || len(s) > CLIENTVERSION_MAX {
		return false
	}
	return CLIENTVERSION_RE.MatchString(s)
}
