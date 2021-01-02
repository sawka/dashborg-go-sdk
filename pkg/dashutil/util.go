package dashutil

import "time"

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
}
