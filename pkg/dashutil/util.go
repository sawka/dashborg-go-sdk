package dashutil

import (
	"errors"
	"time"
)

var TimeoutErr = errors.New("TimeoutErr")
var NoFeStreamErr = errors.New("NoFeStreamErr")

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
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
