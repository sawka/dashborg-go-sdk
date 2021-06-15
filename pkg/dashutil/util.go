package dashutil

import (
	"bytes"
	"encoding/json"
	"errors"
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

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
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
