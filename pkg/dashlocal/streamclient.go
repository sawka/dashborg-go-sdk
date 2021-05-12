package dashlocal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const maxFeStreamSize = 100
const smallDrainSleep = 5 * time.Millisecond
const maxDrainTime = 30 * time.Second

type streamClient struct {
	Lock          *sync.Mutex
	StreamReqMap  map[string]*streamReq       // reqid -> streamReq
	FeStreamMap   map[string]*feStreamControl // feclientid -> feStreamControl
	StreamCloseFn func(reqId string)          // to send a streamclose message to proc

	FeTimeoutShutdownCh chan struct{}
}

type streamReq struct {
	ReqId     string
	StartTime time.Time
	FeClients map[string]bool
}

type feStreamControl struct {
	FeClientId string
	ReturnCh   chan *dashproto.RRAction
	LastDrain  time.Time
	ReqIds     map[string]bool
	PushPanel  string
}

func makeStreamClient(scfn func(reqId string)) *streamClient {
	rtn := &streamClient{
		Lock:                &sync.Mutex{},
		StreamReqMap:        make(map[string]*streamReq),
		FeStreamMap:         make(map[string]*feStreamControl),
		StreamCloseFn:       scfn,
		FeTimeoutShutdownCh: make(chan struct{}),
	}
	go rtn.checkTimeoutsLoop()
	return rtn
}

func (fesc *feStreamControl) nonBlockingSend(rr *dashproto.RRAction) error {
	select {
	case fesc.ReturnCh <- rr:
		return nil
	default:
		return fmt.Errorf("Send would block")
	}
}

func (fesc *feStreamControl) nonBlockingRecv() *dashproto.RRAction {
	select {
	case rtn := <-fesc.ReturnCh:
		return rtn
	default:
		return nil
	}
}

func (fesc *feStreamControl) getReqIds() []string {
	if fesc == nil {
		return nil
	}
	rtn := make([]string, 0, len(fesc.ReqIds))
	for reqId, _ := range fesc.ReqIds {
		rtn = append(rtn, reqId)
	}
	return rtn
}

func (sc *streamClient) shutdown() {
	close(sc.FeTimeoutShutdownCh)
}

func (sc *streamClient) checkTimeoutsLoop() {
	t := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-t.C:
			break

		case <-sc.FeTimeoutShutdownCh:
			return
		}

		sc.checkTimeouts()
	}
}

func (sc *streamClient) checkTimeouts() {
	sc.Lock.Lock()
	defer sc.Lock.Unlock()

	// fmt.Printf("check timeouts req:%d fe:%d\n", len(sc.StreamReqMap), len(sc.FeStreamMap))

	checkDrainTime := time.Now().Add(-maxDrainTime)
	for _, fesc := range sc.FeStreamMap {
		sc.checkFeStream_nolock(fesc, checkDrainTime)
	}
}

func (sc *streamClient) drainFeStream(ctx context.Context, feClientId string, timeout time.Duration, pushPanel string) ([]*dashproto.RRAction, []string, error) {
	sc.Lock.Lock()
	fesc := sc.FeStreamMap[feClientId]
	if fesc != nil {
		fesc.PushPanel = pushPanel
	}
	reqIds := fesc.getReqIds() // nil fesc ok
	sc.Lock.Unlock()
	if fesc == nil && pushPanel == "" {
		return nil, nil, dashutil.NoFeStreamErr
	}
	if fesc == nil && pushPanel != "" {
		sc.Lock.Lock()
		fesc = sc.makeFeStreamControl_nolock(feClientId, pushPanel)
		sc.Lock.Unlock()
	}
	var rtn []*dashproto.RRAction
	for {
		rr := fesc.nonBlockingRecv()
		if rr == nil {
			break
		}
		rtn = append(rtn, rr)
	}
	if len(rtn) > 0 || timeout == 0 {
		return rtn, reqIds, nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	isTimeout := false
	select {
	case <-ctx.Done():
		isTimeout = true
		break
	case <-timer.C:
		isTimeout = true
		break
	case rr := <-fesc.ReturnCh:
		rtn = append(rtn, rr)
		break
	}
	reqIds = sc.updateDrainTime(feClientId)
	if isTimeout {
		return nil, reqIds, dashutil.TimeoutErr
	}
	time.Sleep(smallDrainSleep)
	for {
		rr := fesc.nonBlockingRecv()
		if rr == nil {
			break
		}
		rtn = append(rtn, rr)
	}
	return rtn, reqIds, nil
}

func (sc *streamClient) endReqStream_client_nolock(reqId string, feClientId string) {
	// delete feclient from stream-req
	sreq := sc.StreamReqMap[reqId]
	if sreq != nil {
		delete(sreq.FeClients, feClientId)
	}

	// delete reqId from feClientStreamControl
	fesc := sc.FeStreamMap[feClientId]
	if fesc != nil {
		delete(fesc.ReqIds, reqId)
	}

	// check to see if stream is dead and should be closed
	if sreq != nil && len(sreq.FeClients) == 0 {
		// fmt.Printf("REQ end (client) %s\n", reqId)
		delete(sc.StreamReqMap, reqId)
		sc.StreamCloseFn(reqId)
	}
}

func (sc *streamClient) endReqStream_client(reqId string, feClientId string) {
	sc.Lock.Lock()
	defer sc.Lock.Unlock()

	sc.endReqStream_client_nolock(reqId, feClientId)
}

func (sc *streamClient) updateDrainTime(feClientId string) []string {
	now := time.Now()
	sc.Lock.Lock()
	defer sc.Lock.Unlock()

	fesc := sc.FeStreamMap[feClientId]
	if fesc == nil {
		return nil
	}
	fesc.LastDrain = now
	sc.checkRemoveFeStream_nolock(fesc)
	return fesc.getReqIds()
}

func (sc *streamClient) checkFeStream_nolock(fesc *feStreamControl, checkDrainTime time.Time) {
	feClientId := fesc.FeClientId
	if len(fesc.ReqIds) == 0 && len(fesc.ReturnCh) == 0 && fesc.PushPanel == "" {
		// clean delete
		// fmt.Printf("FESTREAM delete (clean-check) %s\n", feClientId)
		delete(sc.FeStreamMap, feClientId)
		return
	}
	if fesc.LastDrain.After(checkDrainTime) {
		return
	}
	// inactivity
	// fmt.Printf("FESTREAM delete (inactive) %s\n", feClientId)
	delete(sc.FeStreamMap, feClientId)
	for reqId, _ := range fesc.ReqIds {
		sc.endReqStream_client_nolock(reqId, feClientId)
	}
}

func (sc *streamClient) checkRemoveFeStream_nolock(fesc *feStreamControl) {
	if len(fesc.ReqIds) == 0 && len(fesc.ReturnCh) == 0 && fesc.PushPanel == "" {
		// clean delete
		// fmt.Printf("FESTREAM delete (clean-udt) %s\n", fesc.FeClientId)
		delete(sc.FeStreamMap, fesc.FeClientId)
	}
}

func (sc *streamClient) recvResponse(resp *dashproto.SendResponseMessage) (int, error) {
	sc.Lock.Lock()
	defer sc.Lock.Unlock()

	reqId := resp.ReqId
	sreq := sc.StreamReqMap[reqId]
	if sreq == nil {
		return 0, nil
	}
	for feClientId, _ := range sreq.FeClients {
		fesc := sc.FeStreamMap[feClientId]
		if fesc == nil {
			delete(sreq.FeClients, feClientId)
			continue
		}
		for _, rr := range resp.Actions {
			sendErr := fesc.nonBlockingSend(rr)
			if sendErr != nil {
				break
			}
		}
	}
	if resp.ResponseDone {
		// fmt.Printf("REQ end (server) %s\n", reqId)
		delete(sc.StreamReqMap, reqId)
		for feClientId, _ := range sreq.FeClients {
			sc.endReqStream_server_nolock(reqId, feClientId)
		}
	}
	return len(sreq.FeClients), nil
}

func (sc *streamClient) endReqStream_server_nolock(reqId string, feClientId string) {
	fesc := sc.FeStreamMap[feClientId]
	if fesc == nil {
		return
	}
	delete(fesc.ReqIds, reqId)
}

func (sc *streamClient) startStream(existingReqId string, feClientId string) (string, error) {
	sc.Lock.Lock()
	defer sc.Lock.Unlock()

	var sreq *streamReq
	if existingReqId != "" {
		sreq = sc.StreamReqMap[existingReqId]
	}
	if sreq == nil {
		sreq = &streamReq{
			ReqId:     existingReqId,
			StartTime: time.Now(),
			FeClients: make(map[string]bool),
		}
		if sreq.ReqId == "" {
			sreq.ReqId = uuid.New().String()
		}
		// fmt.Printf("REQ create %s\n", sreq.ReqId)
		sc.StreamReqMap[sreq.ReqId] = sreq
	}
	sreq.FeClients[feClientId] = true
	sc.attachReqToFeStream_nolock(feClientId, sreq.ReqId)
	return sreq.ReqId, nil
}

func (sc *streamClient) attachReqToFeStream_nolock(feClientId string, reqId string) {
	fesc := sc.FeStreamMap[feClientId]
	if fesc == nil {
		fesc = sc.makeFeStreamControl_nolock(feClientId, "")
	}
	fesc.ReqIds[reqId] = true
}

func (sc *streamClient) makeFeStreamControl_nolock(feClientId string, pushPanel string) *feStreamControl {
	fesc := &feStreamControl{
		FeClientId: feClientId,
		ReturnCh:   make(chan *dashproto.RRAction, maxFeStreamSize),
		LastDrain:  time.Now(),
		ReqIds:     make(map[string]bool),
		PushPanel:  pushPanel,
	}
	// fmt.Printf("FESTREAM create %s\n", feClientId)
	sc.FeStreamMap[feClientId] = fesc
	return fesc
}

func (sc *streamClient) backendPush(panelName string, rr *dashproto.RRAction) {
	sc.Lock.Lock()
	defer sc.Lock.Unlock()

	for _, fesc := range sc.FeStreamMap {
		if fesc.PushPanel != panelName {
			continue
		}
		// fmt.Printf("BACKENDPUSH %s:%s => %s\n", panelName, rr.Selector, fesc.FeClientId)
		fesc.nonBlockingSend(rr)
	}
}
