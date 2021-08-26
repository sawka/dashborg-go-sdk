package dashutil

import (
	"regexp"
	"strings"
)

const (
	ZoneNameMax      = 20
	ZoneAccessMax    = 50
	ControlNameMax   = 30
	AppNameMax       = 20
	ProcNameMax      = 20
	FileNameMax      = 80
	EmailMax         = 80
	PasswordMax      = 80
	PasswordMin      = 8
	MimeTypeMax      = 80
	Sha256HexLen     = 64
	Sha256Base64Len  = 44
	UuidLen          = 36
	HandlerPathMax   = 100
	DataPathMax      = 200
	PathMax          = 100
	TagMax           = 50
	RoleMax          = 12
	RoleListMax      = 50
	ClientVersionMax = 20
	ProcTagValMax    = 200
	HostDataValMax   = 100
	BlobKeyMax       = 100
	BlobNsMax        = 20
	SimpleIdMax      = 30
	UserIdMax        = 100
)

var (
	zoneNameRe       = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_.-]*$")
	controlNameRe    = regexp.MustCompile("^[a-zA-Z0-9_.:#/-]+$")
	appNameRe        = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_.-]*$")
	procNameRe       = regexp.MustCompile("^[a-zA-Z0-9_.-]+$")
	uuidRe           = regexp.MustCompile("^[a-fA-F0-9-]{36}$")
	handlerPathRe    = regexp.MustCompile("^/\\@?[a-zA-Z0-9_-][a-zA-Z0-9_/-]*$")
	base64Re         = regexp.MustCompile("^[a-zA-Z0-9/+=]+$")
	hexRe            = regexp.MustCompile("^[a-f0-9]+$")
	imageMimeTypeRe  = regexp.MustCompile("^image/[a-z0-9.-]+$")
	mimeTypeRe       = regexp.MustCompile("^[a-z0-9.-]+/[a-z0-9.-]+$")
	simpleFileNameRe = regexp.MustCompile("^[a-zA-Z0-9._-]+$")
	pathRe           = regexp.MustCompile("^/[a-zA-Z0-9._/-]*$")
	tagRe            = regexp.MustCompile("^[a-zA-Z0-9._:/-]+$")
	roleRe           = regexp.MustCompile("^(\\*|[a-z][a-z0-9-]+)$")
	extBlobKeyRe     = regexp.MustCompile("^(?:([a-z][a-z0-9]*):)?([0-9a-zA-Z/_.-]+)$")
	blobKeyRe        = regexp.MustCompile("^[0-9a-zA-Z/_.-]+$")
	blobNsRe         = regexp.MustCompile("^[a-z][a-z0-9]*$")
	simpleIdRe       = regexp.MustCompile("^[a-zA-Z][a-zA-Z0-9_-]*")
	clientVersionRe  = regexp.MustCompile("^([a-z][a-z0-9_]*)-(\\d{1,3})\\.(\\d{1,3})\\.(\\d{1,4})$")
	zoneAccessRe     = regexp.MustCompile("^[a-zA-Z0-9_.*-]+$")

	// https://www.w3.org/TR/2016/REC-html51-20161101/sec-forms.html#email-state-typeemali
	emailRe = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

	userIdRe   = regexp.MustCompile("^[a-z0-9A-Z_.@#-]+$")
	passwordRe = regexp.MustCompile("^[a-zA-Z0-9]+$")
)

var ValidRequestType = map[string]bool{"data": true, "handler": true, "stream": true, "auth": true, "html": true, "init": true}
var ValidActionType = map[string]bool{"setdata": true, "event": true, "invalidate": true, "html": true, "panelauth": true, "panelauthchallenge": true, "error": true, "blob": true, "blobext": true, "streamopen": true, "backendpush": true}
var ValidBlobNs = map[string]bool{"app": true, "html": true}

func IsZoneNameValid(zoneName string) bool {
	if len(zoneName) > ZoneNameMax {
		return false
	}
	return zoneNameRe.MatchString(zoneName)
}

func IsZoneAccessValid(zoneAccess string) bool {
	if len(zoneAccess) > ZoneAccessMax {
		return false
	}
	return zoneAccessRe.MatchString(zoneAccess)
}

func IsAppNameValid(appName string) bool {
	if len(appName) > AppNameMax {
		return false
	}
	return appNameRe.MatchString(appName)
}

func IsSimpleFileNameValid(fileName string) bool {
	if len(fileName) > FileNameMax {
		return false
	}
	return simpleFileNameRe.MatchString(fileName)
}

func IsControlNameValid(controlName string) bool {
	if len(controlName) > ControlNameMax {
		return false
	}
	return controlNameRe.MatchString(controlName)
}

func IsProcNameValid(procName string) bool {
	if len(procName) > ProcNameMax {
		return false
	}
	return procNameRe.MatchString(procName)
}

func IsUUIDValid(uuid string) bool {
	if len(uuid) != UuidLen {
		return false
	}
	return uuidRe.MatchString(uuid)
}

func IsHandlerPathValid(handler string) bool {
	if len(handler) > HandlerPathMax {
		return false
	}
	return handlerPathRe.MatchString(handler)
}

func IsPublicKeyValid(publicKey string) bool {
	if len(publicKey) < 20 || len(publicKey) > 1000 {
		return false
	}
	return base64Re.MatchString(publicKey)
}

func IsSha256HexHashValid(s string) bool {
	if len(s) != Sha256HexLen {
		return false
	}
	return hexRe.MatchString(s)
}

func IsSha256Base64HashValid(s string) bool {
	if len(s) != Sha256Base64Len {
		return false
	}
	return base64Re.MatchString(s)
}

func IsMimeTypeValid(s string) bool {
	if len(s) == 0 || len(s) > MimeTypeMax {
		return false
	}
	return mimeTypeRe.MatchString(s)
}

func IsImageMimeTypeValid(s string) bool {
	if len(s) == 0 || len(s) > MimeTypeMax {
		return false
	}
	return imageMimeTypeRe.MatchString(s)
}

func IsEmailValid(s string) bool {
	if len(s) == 0 || len(s) > EmailMax {
		return false
	}
	return emailRe.MatchString(s)
}

func IsPasswordValid(s string) bool {
	if len(s) == 0 || len(s) > PasswordMax {
		return false
	}
	if len(s) < PasswordMin {
		return false
	}
	return true
}

func IsPathValid(s string) bool {
	if len(s) == 0 || len(s) > PathMax {
		return false
	}
	return pathRe.MatchString(s)
}

func IsRequestTypeValid(s string) bool {
	return ValidRequestType[s]
}

func IsActionTypeValid(s string) bool {
	return ValidActionType[s]
}

func IsTagValid(s string) bool {
	if len(s) == 0 || len(s) > TagMax {
		return false
	}
	return tagRe.MatchString(s)
}

func IsBlobKeyValid(s string) bool {
	if len(s) == 0 || len(s) > BlobKeyMax {
		return false
	}
	return blobKeyRe.MatchString(s)
}

func IsBlobNsValid(s string) bool {
	if len(s) == 0 || len(s) > BlobNsMax {
		return false
	}
	return ValidBlobNs[s]
}

func IsRoleValid(s string) bool {
	if len(s) == 0 || len(s) > RoleMax {
		return false
	}
	return roleRe.MatchString(s)
}

func IsClientVersionValid(s string) bool {
	if len(s) == 0 || len(s) > ClientVersionMax {
		return false
	}
	return clientVersionRe.MatchString(s)
}

func IsSimpleIdValid(s string) bool {
	if len(s) == 0 || len(s) > SimpleIdMax {
		return false
	}
	return simpleIdRe.MatchString(s)
}

func IsRoleListValid(s string) bool {
	if len(s) == 0 || len(s) > RoleListMax {
		return false
	}
	list := strings.Split(s, ",")
	for _, role := range list {
		if !IsRoleValid(role) {
			return false
		}
	}
	return true
}

func IsUserIdValid(s string) bool {
	if len(s) == 0 || len(s) > UserIdMax {
		return false
	}
	return userIdRe.MatchString(s)
}
