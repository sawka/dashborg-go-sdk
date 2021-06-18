package dash

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const feStreamInactiveTimeout = 30 * time.Second

func (sc streamControl) getStreamKey() streamKey {
	return streamKey{PanelName: sc.PanelName, StreamId: sc.StreamOpts.StreamId}
}

func (pc *appClient) stream_lookup_nolock(reqId string) (streamControl, bool) {
	skey, ok := pc.StreamKeyMap[reqId]
	if !ok {
		return streamControl{}, false
	}
	sc, ok := pc.StreamMap[skey]
	return sc, ok
}

func (pc *appClient) stream_hasZeroClients(reqId string) bool {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	sc, ok := pc.stream_lookup_nolock(reqId)
	if !ok {
		return true
	}
	return sc.HasZeroClients
}

func (pc *appClient) stream_clientStartBare(sc streamControl) (streamControl, error) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()

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

func (pc *appClient) stream_getReqId(skey streamKey) string {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()

	sc, ok := pc.StreamMap[skey]
	if !ok {
		return ""
	}
	return sc.ReqId
}

func (pc *appClient) stream_clientStart(newSc streamControl, feClientId string) (streamControl, bool) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
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

func (pc *appClient) stream_clientStop(reqId string) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	pc.stream_deleteAndCancel_nolock(reqId)
}

func (pc *appClient) stream_handleZeroClients(reqId string) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
	pc.stream_handleZeroClients_nolock(reqId)
}

func (pc *appClient) stream_handleZeroClients_nolock(reqId string) {
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

func (pc *appClient) stream_serverStop(reqId string) {
	pc.Lock.Lock()
	defer pc.Lock.Unlock()
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

func (pc *appClient) stream_deleteAndCancel_nolock(reqId string) {
	skey, ok := pc.StreamKeyMap[reqId]
	if !ok {
		return
	}
	sc, ok := pc.StreamMap[skey]
	if !ok {
		pc.logV("No Stream found for key:%v\n", skey)
		return
	}
	sc.CancelFn() // cancel context
	delete(pc.StreamKeyMap, reqId)
	delete(pc.StreamMap, skey)
}

func (pc *appClient) StartBareStream(panelName string, streamOpts StreamOpts) (*PanelRequest, error) {
	if !streamOpts.NoServerCancel {
		return nil, fmt.Errorf("BareStreams must have NoServerCancel set in StreamOpts")
	}
	if !dashutil.IsTagValid(streamOpts.StreamId) {
		return nil, fmt.Errorf("Invalid StreamId")
	}
	sc := streamControl{
		PanelName:      panelName,
		StreamOpts:     streamOpts,
		ReqId:          uuid.New().String(),
		HasZeroClients: true,
	}
	var err error
	sc, err = pc.stream_clientStartBare(sc)
	if err != nil {
		return nil, err
	}
	streamReq := &PanelRequest{
		StartTime:   time.Now(),
		ctx:         sc.Ctx,
		lock:        &sync.Mutex{},
		PanelName:   panelName,
		ReqId:       sc.ReqId,
		RequestType: "stream",
		Path:        streamOpts.StreamId,
	}
	return streamReq, nil
}
