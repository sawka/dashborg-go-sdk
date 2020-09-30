package dash

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/bufsrv"
	"github.com/sawka/dashborg-go-sdk/pkg/transport"
)

const DEFAULT_QUEUESIZE = 100 // DASHBORG_QUEUESIZE

type queueEntry struct {
	Message     interface{}
	RtnCallback func(interface{}, error)
}

type pushFnWrap struct {
	Id string
	Fn pushFn
}

type pushFn func(interface{}) (interface{}, error)

type ProcClient struct {
	CVar *sync.Cond

	StartTs   int64
	ProcRunId string
	Config    *Config

	Client *bufsrv.Client

	Queue   chan queueEntry
	PushMap map[string][]pushFnWrap

	Wg            sync.WaitGroup
	Done          bool // WaitForClear, no more messages can be queued
	QueueClear    bool // the last message has been sent
	Connected     bool // connection is open to BufSrv
	ConnectGiveUp bool // we've given up on trying to reconnect the client

	ActiveControls map[string]bool
}

var Client *ProcClient

func newProcClient() *ProcClient {
	rtn := &ProcClient{}
	rtn.CVar = sync.NewCond(&sync.Mutex{})
	rtn.StartTs = Ts()
	rtn.ProcRunId = uuid.New().String()
	rtn.ActiveControls = make(map[string]bool)

	queueSize := DEFAULT_QUEUESIZE
	if os.Getenv("DASHBORG_QUEUESIZE") != "" {
		var err error
		queueSize, err = strconv.Atoi(os.Getenv("DASHBORG_QUEUESIZE"))
		if err != nil {
			log.Printf("Invalid DASHBORG_QUEUESIZE env var [%s]", os.Getenv("DASHBORG_QUEUESIZE"))
			queueSize = DEFAULT_QUEUESIZE
		}
		// TODO range check
	}
	rtn.Queue = make(chan queueEntry, queueSize)
	rtn.PushMap = make(map[string][]pushFnWrap)
	rtn.Wg = sync.WaitGroup{}
	return rtn
}

func StartProcClient(config *Config) *ProcClient {
	config.SetDefaults()
	if config.AccId == "" {
		panic("dashborg.StartProcClient() cannot start, no AccId specified.  Call UseAnonKeys() or UseKeys() and ensure certificate file is properly formated.")
	}
	// TODO validate config
	Client = newProcClient()
	Client.Config = config
	log.Printf("Dashborg Initialized Client ProcName:%s ProcRunId:%s\n", Client.Config.ProcName, Client.ProcRunId)
	// log.Printf("Dashborg Link %s\n", PanelLink(c.AccId, c.ZoneName, "default"))
	Client.goConnectClient()
	return Client
}

func (pc *ProcClient) WaitForClear() {
	time.Sleep(Client.Config.MinClearTimeout)
	pc.CVar.L.Lock()
	pc.Done = true
	pc.CVar.Broadcast()
	pc.CVar.L.Unlock()

	dm2 := transport.DoneMessage{
		MType: "done",
		Ts:    Ts(),
	}
	Client.SendMessageWithCallback(dm2, func(v interface{}, err error) {
		close(pc.Queue)
		pc.CVar.L.Lock()
		pc.QueueClear = true
		pc.CVar.Broadcast()
		pc.CVar.L.Unlock()
	})
	pc.Wg.Wait()
}

func (c *ProcClient) GetProcRunId() string {
	return c.ProcRunId
}

func (c *ProcClient) SendMessage(m interface{}) error {
	return c.SendMessageWithCallback(m, nil)
}

func (c *ProcClient) SendMessageWithCallback(m interface{}, callback func(interface{}, error)) error {
	isDoneMsg := transport.GetMType(m) == "done"
	c.CVar.L.Lock()
	defer c.CVar.L.Unlock()
	if !isDoneMsg && c.Done {
		return errors.New("ProcClient done, dropping message")
	}
	qe := queueEntry{Message: m, RtnCallback: callback}
	select {
	case c.Queue <- qe:
	default:
		return fmt.Errorf("ProcClient queue full, dropping message")
	}
	return nil

}

func (c *ProcClient) goConnectClient() {
	Client.Wg.Add(1)
	go func() {
		defer Client.Wg.Done()
		go c.retryConnectClient()
		Client.sendLoop()
	}()
}

func (c *ProcClient) sendLoop() {
	var retryEntry *queueEntry
	for {
		// only send if Connected
		c.CVar.L.Lock()
		for !c.Connected && !c.ConnectGiveUp {
			c.CVar.Wait()
		}
		if !c.Connected && c.ConnectGiveUp {
			c.CVar.L.Unlock()
			log.Printf("Dashborg ProcClient SendLoop GiveUp\n")
			break
		}
		c.CVar.L.Unlock()

		var entry queueEntry
		if retryEntry == nil {
			var ok bool
			entry, ok = <-c.Queue
			if !ok {
				break
			}
		} else {
			entry = *retryEntry
			retryEntry = nil
		}

		// deal with error conditions
		resp := c.Client.DoRequest("msg", entry.Message)
		if resp.ConnError != nil {
			log.Printf("Dashborg ProcClient ConnError:%v\n", resp.ConnError)
			retryEntry = &entry
			c.CVar.L.Lock()
			c.Client = nil
			c.Connected = false
			c.CVar.Broadcast()
			c.CVar.L.Unlock()
			LogInfo("Dashborg ProcClient Disconnected, will retry last message\n")
			continue
		} else {
			err := resp.Err()
			if err != nil {
				log.Printf("Dashborg ProcClient err:%v\n", err)
			}
			if entry.RtnCallback != nil {
				go entry.RtnCallback(resp.Response, err)
			}
		}

	}
	log.Printf("Dashborg Client SendLoop Done\n")
}

// retry states
const (
	retryConnectedWait = iota
	retryQueueClear
	retryDisconnectedTryOnce
	retryDisconnectedTry
	retryDisconnectedWait
)

func (pc *ProcClient) getRetryState(lastConnectTry time.Time) (int, time.Duration) {
	if pc.QueueClear {
		return retryQueueClear, 0
	}
	if pc.Connected {
		return retryConnectedWait, 0
	}
	if pc.Done {
		return retryDisconnectedTryOnce, 0
	}
	now := time.Now()
	if lastConnectTry.IsZero() || now.Sub(lastConnectTry) >= 500*time.Millisecond {
		return retryDisconnectedTry, 0
	}
	return retryDisconnectedWait, 500*time.Millisecond - now.Sub(lastConnectTry)
}

func retryStateToString(rs int) string {
	switch rs {
	case retryConnectedWait:
		return "retryConnectedWait"
	case retryQueueClear:
		return "retryQueueClear"
	case retryDisconnectedTryOnce:
		return "retryDisconnectedTryOnce"
	case retryDisconnectedTry:
		return "retryDisconnectedTry"
	case retryDisconnectedWait:
		return "retryDisconnectedWait"
	}
	return ""
}

func (pc *ProcClient) retryWait(d time.Duration) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	if d > 0 {
		go func() {
			time.Sleep(d)
			pc.CVar.Broadcast()
		}()
	}
	pc.CVar.Wait()
}

// retry every 0.5s
// stop retrying after queue is clear or done, must try at least once
func (pc *ProcClient) retryConnectClient() error {
	var lastConnectTry time.Time
	for {
		pc.CVar.L.Lock()
		state, waitDuration := pc.getRetryState(lastConnectTry)
		pc.CVar.L.Unlock()
		// log.Printf("** (%s) Retry State: %s / %v\n", pc.Config.ProcName, retryStateToString(state), waitDuration)
		if state == retryQueueClear {
			break
		}
		if state == retryConnectedWait {
			pc.retryWait(0)
			continue
		}
		if state == retryDisconnectedWait {
			pc.retryWait(waitDuration)
			continue
		}
		lastConnectTry = time.Now()
		err := pc.connectClient()
		if err != nil {
			log.Printf("MFMT ProcClient Error Connecting client err:%v\n", err)
		}
		if err != nil && state == retryDisconnectedTryOnce {
			break
		}
	}
	pc.CVar.L.Lock()
	pc.ConnectGiveUp = true
	pc.CVar.Broadcast()
	pc.CVar.L.Unlock()
	LogInfo("ProcClient RetryConnectClient done\n")
	return nil
}

func (pc *ProcClient) connectClient() error {
	// TODO connect timeout (context?)
	c := pc.Config
	addr := c.BufSrvHost + ":" + strconv.Itoa(c.BufSrvPort)
	var tlsConfig *tls.Config
	cert, err := tls.LoadX509KeyPair(c.CertFileName, c.KeyFileName)
	if err != nil {
		return fmt.Errorf("Cannot load keypair key:%s cert:%s err:%w", c.KeyFileName, c.CertFileName, err)
	}
	tlsConfig = &tls.Config{
		MinVersion:               tls.VersionTLS13,
		CurvePreferences:         []tls.CurveID{tls.CurveP384},
		PreferServerCipherSuites: true,
		InsecureSkipVerify:       true,
		Certificates:             []tls.Certificate{cert},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		},
	}
	LogInfo("Connecting to addr:%s\n", addr)
	client, err := bufsrv.MakeClient(addr, tlsConfig)
	if err != nil {
		return err
	}
	log.Printf("MFMT Client Connected to %s\n", addr)
	err = client.SetPushCtx(pc.ProcRunId, pc.handlePush)
	if err != nil {
		client.Close()
		return err
	}
	err = sendProcMessage(client)
	if err != nil {
		client.Close()
		return err
	}
	pc.CVar.L.Lock()
	pc.Client = client
	pc.Connected = true
	pc.CVar.Broadcast()
	pc.CVar.L.Unlock()
	return nil
}

func (c *ProcClient) handlePush(p bufsrv.PushType) (interface{}, error) {
	LogInfo("handle push %#v\n", p)
	c.CVar.L.Lock()
	pfns := c.PushMap[p.PushRecvId]
	pfnsCopy := make([]pushFnWrap, len(pfns))
	copy(pfnsCopy, pfns)
	c.CVar.L.Unlock()
	if len(pfnsCopy) == 0 {
		return nil, fmt.Errorf("No receiver registered for PushRecvId[%s]", p.PushRecvId)
	}
	for _, pfn := range pfnsCopy {
		rtn, err := pfn.Fn(p.PushPayload)
		if err != nil {
			return nil, err
		}
		if rtn != nil {
			return rtn, err
		}
	}
	return nil, nil
}

func sendProcMessage(client *bufsrv.Client) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	pm := transport.ProcMessage{
		MType:     "proc",
		Ts:        Ts(),
		AccId:     Client.Config.AccId,
		AnonAcc:   Client.Config.AnonAcc,
		ProcName:  Client.Config.ProcName,
		ProcIName: Client.Config.ProcIName,
		ProcTags:  Client.Config.ProcTags,
		ProcRunId: Client.ProcRunId,
		ZoneName:  Client.Config.ZoneName,
		StartTs:   Client.StartTs,
		HostData: map[string]string{
			"HostName": hostname,
			"Pid":      strconv.Itoa(os.Getpid()),
		},
		// ActiveControls: Cache.GetActiveControls(),
	}
	resp := client.DoRequest("msg", pm)
	return resp.Err()
}

func (pc *ProcClient) addToActiveControls(controlLoc string) {
	pc.CVar.L.Lock()
	defer pc.CVar.L.Unlock()
	pc.ActiveControls[controlLoc] = true
}
