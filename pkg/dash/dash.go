package dash

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sync"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
)

type Config struct {
	// DASHBORG_ACCID, set to force an AccountId (must match certificate).  If not set, AccountId is set from certificate file.
	// If AccId is given and AutoKeygen is true, and key/cert files are not found, Dashborg will create a new self-signed
	//     keypair using the AccId given.
	// If AccId is given, and the certificate does not match, this will cause a panic.
	AccId string

	// Set to true for unregistered accounts
	AnonAcc bool

	// DASHBORG_ZONE defaults to "default"
	ZoneName string

	// Process Name Attributes.  Only ProcName is required
	ProcName string // DASHBORG_PROCNAME (set from executable filename if not set)
	ProcTags map[string]string

	KeyFileName  string // DASHBORG_KEYFILE private key file
	CertFileName string // DASHBORG_CERTFILE certificate file, CN must be set to your Dashborg Account Id.

	// Create a self-signed key/cert if they do not exist.  This will also create a random Account Id.
	// Should only be used with AnonAcc is true, and AccId is not set
	AutoKeygen bool

	// The minimum amount of time to wait for all events to complete processing before shutting down after calling WaitForClear()
	// Defaults to 1 second.
	MinClearTimeout time.Duration

	// DASHBORG_VERBOSE, set to true for extra debugging information
	Verbose bool

	// These are for internal testing, should not normally be set by clients.
	Env             string // DASHBORG_ENV
	DashborgSrvHost string // DASHBORG_PROCHOST
	DashborgSrvPort int    // DASHBORG_PROCPORT
}

type PanelRequest struct {
	Ctx        context.Context
	Lock       *sync.Mutex // synchronizes RRActions
	PanelName  string
	ReqId      string
	FeClientId string
	Path       string
	Data       interface{}
	RRActions  []*dashproto.RRAction
	Err        error
	IsDone     bool
}

type HandlerOpt interface {
	HandlerOptType() string
}

func Ts() int64 {
	return time.Now().UnixNano() / 1000000
}

func DefinePanel(panelName string, html string) error {
	ts := Ts()
	runFn := func(req *PanelRequest) error {
		htmlAction := &dashproto.RRAction{
			Ts:         ts,
			ActionType: "panel",
			Html:       html,
		}
		req.appendRR(htmlAction)
		return nil
	}
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "panel",
		Path:        "",
	}
	Client.registerHandler(hkey, runFn)
	return nil
}

// returns contents, fileinfo, use-cached, err
func readConditionally(fileName string, finfo os.FileInfo) (string, os.FileInfo, bool, error) {
	newFinfo, err := os.Stat(fileName)
	if err != nil {
		return "", nil, false, err
	}
	shouldReload := finfo == nil || finfo.Size() != newFinfo.Size() || finfo.ModTime() != newFinfo.ModTime()

	fd, err := os.Open(fileName)
	if err != nil {
		return "", nil, false, err
	}
	defer fd.Close()
	if !shouldReload {
		return "", finfo, true, nil
	}
	htmlBytes, err := ioutil.ReadAll(fd)
	if err != nil {
		return "", nil, false, err
	}
	return string(htmlBytes), finfo, false, nil
}

func DefinePanelFromFile(panelName string, fileName string, pollTime time.Duration) error {
	html, finfo, _, err := readConditionally(fileName, nil)
	if err != nil {
		return err
	}
	log.Printf("DefinePanel loaded panel:%s from file:%s len:%d\n", panelName, fileName, len(html))
	runFn := func(req *PanelRequest) error {
		ts := Ts()
		htmlAction := &dashproto.RRAction{
			Ts:         ts,
			ActionType: "panel",
			Html:       html,
		}
		req.appendRR(htmlAction)
		return nil
	}
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "panel",
		Path:        "",
	}
	Client.registerHandler(hkey, runFn)
	if pollTime > 0 {
		if pollTime < 200*time.Millisecond {
			pollTime = 200 * time.Millisecond
		}
		go func() {
			ticker := time.NewTicker(pollTime)
			var lastError error
			for {
				select {
				case <-ticker.C:
				}
				newHtml, newFinfo, cached, err := readConditionally(fileName, finfo)
				if err != nil && lastError == nil {
					lastError = err
					log.Printf("Error polling panel:%s file:%s for changes: %v\n", panelName, fileName, err)
					continue
				}
				if cached {
					continue
				}
				if html != newHtml {
					html = newHtml
					finfo = newFinfo
					log.Printf("DefinePanel reloaded panel:%s file:%s len:%d\n", panelName, fileName, len(html))
				}
			}
		}()
	}
	return nil
}

func (req *PanelRequest) appendRR(rrAction *dashproto.RRAction) {
	req.Lock.Lock()
	defer req.Lock.Unlock()
	req.RRActions = append(req.RRActions, rrAction)
}

func (req *PanelRequest) SetData(path string, data interface{}) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call SetData(), path=%s, PanelRequest is already done", path)
	}
	jsonData, err := marshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SetData, path:%s, err:%v\n", path, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         Ts(),
		ActionType: "setdata",
		Selector:   path,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *PanelRequest) SendEvent(selector string, eventType string, data interface{}) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call SendEvent(), selector=%s, event=%s, PanelRequest is already done", selector, eventType)
	}
	jsonData, err := marshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SendEvent, selector:%s, event:%s, err:%v\n", selector, eventType, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         Ts(),
		ActionType: "event",
		Selector:   selector,
		EventType:  eventType,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *PanelRequest) Flush() error {
	if req.IsDone {
		return fmt.Errorf("Cannot Flush(), PanelRequest is already done")
	}
	return Client.sendRequestResponse(req, false)
}

func (req *PanelRequest) Done() error {
	if req.IsDone {
		return nil
	}
	return Client.sendRequestResponse(req, true)
}

func RegisterPanelHandler(panelName string, path string, handlerFn func(*PanelRequest) error, opts ...HandlerOpt) {
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "handler",
		Path:        path,
	}
	Client.registerHandler(hkey, handlerFn)
}

func RegisterPanelData(panelName string, path string, handlerFn func(*PanelRequest) error, opts ...HandlerOpt) {
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "data",
		Path:        path,
	}
	Client.registerHandler(hkey, handlerFn)
}
