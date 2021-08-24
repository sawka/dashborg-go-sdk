package dashcloud

import (
	"time"
)

type expoWait struct {
	ForceWait       bool
	InitialWait     time.Time
	CurWaitDeadline time.Time
	LastOkMs        int64
	WaitTimes       int
	CloudClient     *DashCloudClient
}

func (w *expoWait) Wait() bool {
	hasInitialWait := !w.InitialWait.IsZero()
	if w.InitialWait.IsZero() {
		w.InitialWait = time.Now()
	}
	if w.ForceWait || hasInitialWait {
		time.Sleep(1 * time.Second)
		w.WaitTimes++
		w.ForceWait = false
	}
	msWait := int64(time.Since(w.InitialWait)) / int64(time.Millisecond)
	if !hasInitialWait {
		w.LastOkMs = msWait
		return true
	}
	diffWait := msWait - w.LastOkMs
	var rtnOk bool
	switch {
	case msWait < 4000:
		w.LastOkMs = msWait
		rtnOk = true

	case msWait < 60000 && diffWait > 4800:
		w.LastOkMs = msWait
		rtnOk = true

	case diffWait > 29500:
		w.LastOkMs = msWait
		rtnOk = true
	}
	if rtnOk {
		w.CloudClient.logV("DashborgCloudClient RunRequestStreamLoop trying to connect %0.1fs\n", float64(msWait)/1000)
	}
	return rtnOk
}

func (w *expoWait) Reset() {
	*w = expoWait{CloudClient: w.CloudClient}
}
