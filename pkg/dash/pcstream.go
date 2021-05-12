package dash

import (
	"context"
	"fmt"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
)

const feStreamInactiveTimeout = 30 * time.Second

func (sc streamControl) getStreamKey() streamKey {
	return streamKey{PanelName: sc.PanelName, StreamId: sc.StreamOpts.StreamId}
}

func (pc *procClient) stream_lookup_nolock(reqId string) (streamControl, bool) {
	skey, ok := pc.StreamKeyMap[reqId]
	if !ok {
		return streamControl{}, false
	}
	sc, ok := pc.StreamMap[skey]
	return sc, ok
}

func (pc *procClient) stream_hasZeroClients(reqId string) bool {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	sc, ok := pc.stream_lookup_nolock(reqId)
	if !ok {
		return true
	}
	return sc.HasZeroClients
}

func (pc *procClient) stream_clientStartBare(sc streamControl) (streamControl, error) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()

	skey := sc.getStreamKey()
	oldSc, ok := pc.StreamMap[skey]
	if ok {
		return oldSc, fmt.Errorf("Stream already exists")
	}
	ctx, cancel := context.WithCancel(context.Background())
	sc.Ctx = ctx
	sc.CancelFn = cancel
	pc.StreamKeyMap[sc.ReqId] = skey
	pc.StreamMap[skey] = sc
	return sc, nil
}

func (pc *procClient) stream_getReqId(skey streamKey) string {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()

	sc, ok := pc.StreamMap[skey]
	if !ok {
		return ""
	}
	return sc.ReqId
}

func (pc *procClient) stream_clientStart(newSc streamControl, feClientId string) (streamControl, bool) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	skey := newSc.getStreamKey()
	sc, ok := pc.StreamMap[skey]
	var shouldStart bool
	if ok {
		shouldStart = false
		sc.HasZeroClients = false
	} else {
		shouldStart = true
		sc = newSc
		ctx, cancel := context.WithCancel(context.Background())
		sc.Ctx = ctx
		sc.CancelFn = cancel
		pc.StreamKeyMap[sc.ReqId] = skey
	}
	pc.StreamMap[skey] = sc
	return sc, shouldStart
}

func (pc *procClient) stream_clientStop(reqId string) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	pc.stream_deleteAndCancel_nolock(reqId)
}

func (pc *procClient) stream_handleZeroClients(reqId string) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	pc.stream_handleZeroClients_nolock(reqId)
}

func (pc *procClient) stream_handleZeroClients_nolock(reqId string) {
	sc, ok := pc.stream_lookup_nolock(reqId)
	if !ok {
		return
	}
	sc.HasZeroClients = true
	pc.StreamMap[streamKey{sc.PanelName, sc.StreamOpts.StreamId}] = sc
	if sc.StreamOpts.NoServerCancel {
		return
	}
	pc.stream_deleteAndCancel_nolock(reqId)
}

func (pc *procClient) stream_serverStop(reqId string) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	sc, ok := pc.stream_lookup_nolock(reqId)
	if !ok {
		return
	}
	if sc.StreamOpts.NoServerCancel {
		sc.HasZeroClients = true
		pc.StreamMap[streamKey{sc.PanelName, sc.StreamOpts.StreamId}] = sc
		return
	}
	pc.stream_deleteAndCancel_nolock(reqId)
}

func nonBlockingSend(returnCh chan *dashproto.RRAction, rrAction *dashproto.RRAction) error {
	select {
	case returnCh <- rrAction:
		return nil
	default:
		return fmt.Errorf("Send would block")
	}
}

func nonBlockingRecv(returnCh chan *dashproto.RRAction) *dashproto.RRAction {
	select {
	case rtn := <-returnCh:
		return rtn
	default:
		return nil
	}
}

func (pc *procClient) stream_deleteAndCancel_nolock(reqId string) {
	skey, ok := pc.StreamKeyMap[reqId]
	if !ok {
		return
	}
	sc, ok := pc.StreamMap[skey]
	if !ok {
		logV("No Stream found for key:%v\n", skey)
		return
	}
	sc.CancelFn() // cancel context
	delete(pc.StreamKeyMap, reqId)
	delete(pc.StreamMap, skey)
}
