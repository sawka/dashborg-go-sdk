package dash

import (
	"context"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

type LocalProcClient interface {
	DispatchLocalRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) ([]*dashproto.RRAction, error)
	GetClientVersion() string
	DrainLocalFeStream(ctx context.Context, feClientId string, timeout time.Duration, pushPanel string) ([]*dashproto.RRAction, []string, error)
	StopStream(reqId string) error
}

func (pc *procClient) DispatchLocalRequest(ctx context.Context, reqMsg *dashproto.RequestMessage) ([]*dashproto.RRAction, error) {
	return pc.dispatchLocalRequest(ctx, reqMsg)
}

func (pc *procClient) GetClientVersion() string {
	return CLIENT_VERSION
}

func (pc *procClient) DrainLocalFeStream(ctx context.Context, feClientId string, timeout time.Duration, pushPanel string) ([]*dashproto.RRAction, []string, error) {
	returnCh, reqIds := pc.getLocalFeStream(feClientId, true, pushPanel, false)
	if returnCh == nil && pushPanel == "" {
		return nil, nil, dashutil.NoFeStreamErr
	}
	var rtn []*dashproto.RRAction
	for {
		rr := nonBlockingRecv(returnCh)
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
	case <-timer.C:
		isTimeout = true
		break
	case rr := <-returnCh:
		rtn = append(rtn, rr)
		break
	}
	time.Sleep(smallDrainSleep)
	for {
		rr := nonBlockingRecv(returnCh)
		if rr == nil {
			break
		}
		rtn = append(rtn, rr)
	}
	reqIds = pc.localStream_updateDrainTime(feClientId)
	if isTimeout && len(rtn) == 0 {
		return nil, reqIds, dashutil.TimeoutErr
	}
	return rtn, reqIds, nil
}

func (pc *procClient) StopStream(reqId string) error {
	pc.stream_serverStop(reqId)
	return nil
}
