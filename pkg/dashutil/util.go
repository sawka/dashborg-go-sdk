package dashutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
	"unicode"
)

var TimeoutErr = errors.New("TimeoutErr")
var NoFeStreamErr = errors.New("NoFeStreamErr")

type SortSpec struct {
	Column string `json:"column"`
	Asc    bool   `json:"asc"`
}

type ClientVersion struct {
	Valid        bool
	ClientType   string
	MajorVersion int
	MinorVersion int
	PatchVersion int
}

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
}

func DashTime(t time.Time) int64 {
	return t.UnixNano() / 1000000
}

func GoTime(ts int64) time.Time {
	return time.Unix(ts/1000, (ts%1000)*1000000)
}

func MarshalJson(val interface{}) (string, error) {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(val)
	if err != nil {
		return "", err
	}
	return jsonBuf.String(), nil
}

func MarshalJsonNoError(val interface{}) string {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(val)
	if err != nil {
		return "\"error marshaling json\""
	}
	return jsonBuf.String()
}

// Creates a Dashborg compatible double quoted string for pure ASCII printable strings (+ tab, newline, linefeed).
// Not a general purpose string quoter, but will work for most simple keys.
func QuoteString(str string) string {
	var buf bytes.Buffer
	buf.WriteByte('"')
	for i := 0; i < len(str); i++ {
		ch := str[i]
		if ch > unicode.MaxASCII || ch == 127 {
			buf.WriteByte('_')
			continue
		}
		switch ch {
		case '\t':
			buf.WriteByte('\\')
			buf.WriteByte('t')
			continue

		case '\n':
			buf.WriteByte('\\')
			buf.WriteByte('n')
			continue

		case '\r':
			buf.WriteByte('\\')
			buf.WriteByte('r')
			continue

		case '\\':
			buf.WriteByte('\\')
			buf.WriteByte('\\')
			continue
		}
		if !strconv.IsPrint(rune(ch)) {
			buf.WriteByte('_')
			continue
		}
		buf.WriteByte(ch)
	}
	buf.WriteByte('"')
	return buf.String()
}

// checks for dups
func AddToStringArr(arr []string, val string) []string {
	for _, s := range arr {
		if s == val {
			return arr
		}
	}
	return append(arr, val)
}

func RemoveFromStringArr(arr []string, val string) []string {
	pos := -1
	for idx, v := range arr {
		if v == val {
			pos = idx
			break
		}
	}
	if pos == -1 {
		return arr
	}
	arr[pos] = arr[len(arr)-1]
	return arr[:len(arr)-1]
}

func DefaultString(opts ...string) string {
	for _, s := range opts {
		if s != "" {
			return s
		}
	}
	return ""
}

func EnvOverride(val bool, varName string) bool {
	envVal := os.Getenv(varName)
	if envVal == "0" {
		return false
	}
	if envVal == "" {
		return val
	}
	return true
}

func ConvertToString(valArg interface{}) (string, error) {
	if valArg == nil {
		return "", nil
	}
	switch val := valArg.(type) {
	case []byte:
		return string(val), nil

	case string:
		return val, nil

	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", val), nil

	default:
		return "", fmt.Errorf("Invalid base type: %T", val)
	}
}

func MakeAppPath(zoneName string, appName string) string {
	if zoneName == "default" && appName == "default" {
		return "/"
	}
	if zoneName == "default" {
		return fmt.Sprintf("/app/%s", appName)
	}
	if appName == "default" {
		return fmt.Sprintf("/zone/%s", zoneName)
	}
	return fmt.Sprintf("/zone/%s/app/%s", zoneName, appName)
}

func ParseClientVersion(version string) ClientVersion {
	match := clientVersionRe.FindStringSubmatch(version)
	if match == nil {
		return ClientVersion{}
	}
	rtn := ClientVersion{Valid: true}
	rtn.ClientType = match[1]
	rtn.MajorVersion, _ = strconv.Atoi(match[2])
	rtn.MinorVersion, _ = strconv.Atoi(match[3])
	rtn.PatchVersion, _ = strconv.Atoi(match[4])
	return rtn
}

func (v ClientVersion) String() string {
	if !v.Valid {
		return ""
	}
	return fmt.Sprintf("%s-%d.%d.%d", v.ClientType, v.MajorVersion, v.MinorVersion, v.PatchVersion)
}

// returns key-namespace (can be ""), key, err
func ParseExtBlobKey(extBlobKey string) (string, string, error) {
	match := extBlobKeyRe.FindStringSubmatch(extBlobKey)
	if match == nil {
		return "", "", fmt.Errorf("Invalid BlobKey")
	}
	return match[1], match[2], nil
}

func Sha256Base64(barr []byte) string {
	hashVal := sha256.Sum256(barr)
	hashValStr := base64.StdEncoding.EncodeToString(hashVal[:])
	return hashValStr
}

func ParseFullPath(fullPath string) (string, string, string, error) {
	if fullPath == "" {
		return "", "", "", fmt.Errorf("Path cannot be empty")
	}
	if len(fullPath) > FullPathMax {
		return "", "", "", fmt.Errorf("Path too long")
	}
	if fullPath[0] != '/' {
		return "", "", "", fmt.Errorf("Path must begin with '/'")
	}
	match := fullPathRe.FindStringSubmatch(fullPath)
	if match == nil {
		return "", "", "", fmt.Errorf("Invalid Path '%s'", fullPath)
	}
	path := match[2]
	if path == "" {
		path = "/"
	}
	return match[1], path, match[3], nil
}
