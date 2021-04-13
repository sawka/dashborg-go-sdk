package dash

import (
	"context"
	"fmt"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const feStreamInactiveTimeout = 30 * time.Second

func (sc streamControl) getStreamKey() streamKey {
	return streamKey{PanelName: sc.PanelName, StreamId: sc.StreamOpts.StreamId}
}

func (pc *procClient) stream_printStatus() {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()

	fmt.Printf("\nSTREAM STATUS\n")
	for _, sc := range pc.StreamMap {
		fmt.Printf("req %s hzc:%v len:%d\n", sc.ReqId, sc.HasZeroClients, len(sc.LocalFeClients))
	}
	for _, fesc := range pc.LocalFeStreams {
		fmt.Printf("fe  %s len:%2d chlen:%2d (%s) sec:%3d %s\n", fesc.FeClientId, len(fesc.ReqIds), len(fesc.ReturnCh), fesc.LastDrain.Format("15:04:05"), int(time.Since(fesc.LastDrain)/time.Second), fesc.PushPanel)
	}
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
	if pc.localMode() {
		sc.LocalFeClients = append(sc.LocalFeClients, feClientId)
		pc.streamfe_attachReq_nolock(feClientId, sc.ReqId)
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
		localClients := sc.LocalFeClients
		sc.LocalFeClients = nil
		pc.StreamMap[streamKey{sc.PanelName, sc.StreamOpts.StreamId}] = sc
		for _, feClientId := range localClients {
			pc.streamfe_removeReq_nolock(feClientId, reqId)
		}
		return
	}
	pc.stream_deleteAndCancel_nolock(reqId)
}

func (pc *procClient) localStream_timeout_nolock(feClientId string) {
	fesc := pc.LocalFeStreams[feClientId]
	if fesc == nil {
		return
	}
	delete(pc.LocalFeStreams, feClientId)
	for _, reqId := range fesc.ReqIds {
		pc.localStream_removeFeClient_nolock(reqId, feClientId)
	}
}

func (pc *procClient) localStream_checkTimeouts() {
	now := time.Now()
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	for _, fesc := range pc.LocalFeStreams {
		sinceLastDrain := now.Sub(fesc.LastDrain)
		if sinceLastDrain > feStreamInactiveTimeout {
			pc.localStream_timeout_nolock(fesc.FeClientId)
		}
	}
}

func (pc *procClient) getLocalFeStream(feClientId string, updateDrainTime bool, pushPanel string, forceCreate bool) (chan *dashproto.RRAction, []string) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()

	fesc := pc.LocalFeStreams[feClientId]
	if fesc == nil {
		if !forceCreate && pushPanel == "" {
			return nil, nil
		}
		fesc = &feStreamControl{
			FeClientId: feClientId,
			ReturnCh:   make(chan *dashproto.RRAction, returnChSize),
			LastDrain:  time.Now(),
			PushPanel:  pushPanel,
		}
		pc.LocalFeStreams[feClientId] = fesc
	}
	if updateDrainTime {
		fesc.LastDrain = time.Now()
	}
	fesc.PushPanel = pushPanel
	rtnSlice := make([]string, len(fesc.ReqIds))
	copy(rtnSlice, fesc.ReqIds)
	return fesc.ReturnCh, rtnSlice
}

func (pc *procClient) streamfe_attachReq_nolock(feClientId string, reqId string) {
	fesc := pc.LocalFeStreams[feClientId]
	if fesc == nil {
		fesc = &feStreamControl{
			FeClientId: feClientId,
			ReturnCh:   make(chan *dashproto.RRAction, returnChSize),
			LastDrain:  time.Now(),
		}
		pc.LocalFeStreams[feClientId] = fesc
	}
	fesc.ReqIds = dashutil.AddToStringArr(fesc.ReqIds, reqId)
}

func (pc *procClient) streamfe_removeReq_nolock(feClientId string, reqId string) {
	fesc := pc.LocalFeStreams[feClientId]
	if fesc == nil {
		return
	}
	fesc.ReqIds = dashutil.RemoveFromStringArr(fesc.ReqIds, reqId)
	pc.streamfe_checkRemove_nolock(feClientId)
}

func (pc *procClient) streamfe_checkRemove_nolock(feClientId string) {
	fesc := pc.LocalFeStreams[feClientId]
	if fesc == nil {
		return
	}
	if len(fesc.ReqIds) == 0 && len(fesc.ReturnCh) == 0 && fesc.PushPanel == "" {
		delete(pc.LocalFeStreams, feClientId)
	}
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

func (pc *procClient) localStream_updateDrainTime(feClientId string) []string {
	now := time.Now()
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()

	fesc := pc.LocalFeStreams[feClientId]
	if fesc == nil {
		return nil
	}
	pc.streamfe_checkRemove_nolock(feClientId)
	fesc.LastDrain = now
	rtnIds := make([]string, len(fesc.ReqIds))
	copy(rtnIds, fesc.ReqIds)
	return rtnIds
}

func (pc *procClient) sendLocalStreamResponse(m *dashproto.SendResponseMessage) (int, error) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()

	skey, ok := pc.StreamKeyMap[m.ReqId]
	if !ok {
		return 0, fmt.Errorf("No stream key with reqid:%s found", m.ReqId)
	}
	scontrol, ok := pc.StreamMap[skey]
	if !ok {
		return 0, fmt.Errorf("No stream control with reqid:%s found", m.ReqId)
	}
	var toRemove []string
	for _, feClientId := range scontrol.LocalFeClients {
		fesc := pc.LocalFeStreams[feClientId]
		if fesc == nil {
			toRemove = append(toRemove, feClientId)
			continue
		}
		for _, rr := range m.Actions {
			rr.ReqId = m.ReqId
			err := nonBlockingSend(fesc.ReturnCh, rr)
			if err != nil {
				// log.Printf("FeClient stream channel is full reqid:%s feclientid:%s\n", m.ReqId, feClientId)
				break
			}
		}
	}
	for _, feClientId := range toRemove {
		scontrol.LocalFeClients = dashutil.RemoveFromStringArr(scontrol.LocalFeClients, feClientId)
	}
	if m.ResponseDone {
		pc.stream_deleteAndCancel_nolock(m.ReqId)
	}
	return len(scontrol.LocalFeClients), nil
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

	if pc.localMode() {
		// don't need to copy LocalFeClients because already removed from StreamMap
		for _, feClientId := range sc.LocalFeClients {
			pc.streamfe_removeReq_nolock(feClientId, reqId)
		}
	}
}

func (pc *procClient) localStream_removeFeClient_nolock(reqId string, feClientId string) {
	skey, ok := pc.StreamKeyMap[reqId]
	if !ok {
		return
	}
	sc, ok := pc.StreamMap[skey]
	if !ok {
		return
	}
	sc.LocalFeClients = dashutil.RemoveFromStringArr(sc.LocalFeClients, feClientId)
	if len(sc.LocalFeClients) == 0 {
		pc.stream_handleZeroClients(reqId)
	}
}
