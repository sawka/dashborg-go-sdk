package dash

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

func (sc streamControl) getStreamKey() streamKey {
	return streamKey{AppName: sc.AppName, StreamId: sc.StreamOpts.StreamId}
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
	if ok {
		// stream exists
		if feClientId == "" {
			return sc, false
		}
		if sc.HasZeroClients {
			sc.HasZeroClients = false
			pc.StreamMap[skey] = sc
		}
		return sc, false
	}

	// stream does not exist, use newSc
	ctx, cancel := context.WithCancel(context.Background())
	newSc.Ctx = ctx
	newSc.CancelFn = cancel
	pc.StreamKeyMap[newSc.ReqId] = skey
	pc.StreamMap[skey] = newSc
	return newSc, true
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
	pc.StreamMap[streamKey{sc.AppName, sc.StreamOpts.StreamId}] = sc
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
		pc.StreamMap[streamKey{sc.AppName, sc.StreamOpts.StreamId}] = sc
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

func (pc *appClient) connectStream(appName string, streamOpts StreamOpts, feClientId string) (string, error) {
	existingReqId := pc.stream_getReqId(streamKey{AppName: appName, StreamId: streamOpts.StreamId})
	m := &dashproto.StartStreamMessage{
		Ts:            dashutil.Ts(),
		AppId:         &dashproto.AppId{AppName: appName},
		FeClientId:    feClientId,
		ExistingReqId: existingReqId,
	}
	return pc.Api.StartStreamProtoRpc(m)
}

// If feClientId is "", then this starts a "bare" stream (not connected to any frontend client).
// If feClientId is set, then, this will connect this stream to the server, caller must still send "streamopen" action.
// returns (stream-req, stream-reqid, error)
// if stream-req is nil, then the stream already exists with reqid = stream-reqid.
// if stream-req is not nil, then this is a new stream (stream-reqid == stream-req.reqid)
func (pc *appClient) StartStream(appName string, streamOpts StreamOpts, feClientId string) (*AppRequest, string, error) {
	if streamOpts.StreamId == "" {
		streamOpts.StreamId = uuid.New().String()
	}
	if !dashutil.IsTagValid(streamOpts.StreamId) {
		return nil, "", fmt.Errorf("Invalid StreamId")
	}
	if feClientId == "" && !streamOpts.NoServerCancel {
		return nil, "", fmt.Errorf("BareStreams (no FE client) must have NoServerCancel set in StreamOpts")
	}
	var reqId string
	if feClientId != "" {
		if !dashutil.IsUUIDValid(feClientId) {
			return nil, "", fmt.Errorf("Invalid FeClientId")
		}
		var err error
		reqId, err = pc.connectStream(appName, streamOpts, feClientId)
		if err != nil {
			return nil, "", err
		}
	} else {
		reqId = uuid.New().String()
	}
	newSc := streamControl{
		AppName:        appName,
		StreamOpts:     streamOpts,
		ReqId:          reqId,
		HasZeroClients: (feClientId == ""),
	}
	sc, shouldStart := pc.stream_clientStart(newSc, feClientId)
	if !shouldStart {
		return nil, sc.ReqId, nil
	}
	streamReq := &AppRequest{
		info: RequestInfo{
			StartTime:   time.Now(),
			ReqId:       sc.ReqId,
			RequestType: "stream",
			Path:        streamOpts.StreamId,
			AppName:     appName,
			// no feclientid set
		},
		ctx:       sc.Ctx,
		lock:      &sync.Mutex{},
		appClient: pc,
		api:       pc.Api,
	}
	return streamReq, sc.ReqId, nil
}
