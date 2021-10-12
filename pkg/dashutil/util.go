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
	"strings"
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

func MarshalJsonIndent(val interface{}) (string, error) {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
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

func Sha256Base64(barr []byte) string {
	hashVal := sha256.Sum256(barr)
	hashValStr := base64.StdEncoding.EncodeToString(hashVal[:])
	return hashValStr
}

type MultiErr struct {
	Errs []error
}

func (merr MultiErr) Error() string {
	errStrs := make([]string, len(merr.Errs))
	for idx, err := range merr.Errs {
		errStrs[idx] = err.Error()
	}
	return fmt.Sprintf("Multiple Errors (%d): [%s]", len(merr.Errs), strings.Join(errStrs, " | "))
}

func ConvertErrArray(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return MultiErr{Errs: errs}
}

func MakeHtmlPage(path string, pageName string) string {
	if path == "" {
		return pageName
	}
	if pageName == "" {
		return path
	}
	return fmt.Sprintf("%s | %s", path, pageName)
}

// returns (path, pageName, error)
func ParseHtmlPage(displayStr string) (string, string, error) {
	fields := strings.Split(displayStr, "|")
	if len(fields) != 1 && len(fields) != 2 {
		return "", "", fmt.Errorf("Invalid display, format as either [path], [pagename], or [path]|[pagename]")
	}
	var pathStr, pageName string
	if len(fields) == 1 {
		f1 := strings.TrimSpace(fields[0])
		if strings.HasPrefix(f1, "/") {
			pathStr = f1
			pageName = "default"
		} else {
			pageName = f1
		}
	} else {
		pathStr = strings.TrimSpace(fields[0])
		pageName = strings.TrimSpace(fields[1])
	}
	if pathStr != "" {
		err := ValidateFullPath(pathStr, true)
		if err != nil {
			return "", "", err
		}
	}
	if !IsSimpleIdValid(pageName) {
		return "", "", fmt.Errorf("Invalid page name '%s'", pageName)
	}
	return pathStr, pageName, nil
}
