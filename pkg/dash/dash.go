package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
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

	KeyFileName  string // DASHBORG_KEYFILE private key file (defaults to dashborg-client.key)
	CertFileName string // DASHBORG_CERTFILE certificate file, CN must be set to your Dashborg Account Id.  (defaults to dashborg-client.crt)

	// Create a self-signed key/cert if they do not exist.  This will also create a random Account Id.
	// Should only be used with AnonAcc is true.  If AccId is set, will create a key with that AccId
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

// PanelRequest encapsulates all the data about a Dashborg request.  Normally the only
//   fields that a handler needs to access are "Data" and "Model" in order to read
//   the parameters and UI state associated with this request.  The other fields are
//   exported, but subject to change and should not be used except in advanced use cases.
type PanelRequest struct {
	Ctx         context.Context // gRPC context
	Lock        *sync.Mutex     // synchronizes RRActions
	PanelName   string
	ReqId       string                // unique request id
	RequestType string                // "data" or "handler"
	FeClientId  string                // unique id for client (not set for normal requests)
	Path        string                // handler or data path
	Data        interface{}           // json-unmarshaled data attached to this request
	Model       interface{}           // json-unmarshaled model for this request
	AuthData    []*authAtom           // authentication tokens associated with this request
	RRActions   []*dashproto.RRAction // output, these are the actions that will be returned
	Err         error                 // set if an error occured (when set, RRActions are not sent)
	IsDone      bool                  // set after Done() is called and response has been sent to server
	AuthImpl    bool                  // if not set, will default NoAuth() on Done()
}

func panelLink(panelName string) string {
	accId := globalClient.Config.AccId
	zoneName := globalClient.Config.ZoneName
	if globalClient.Config.Env == "dev" {
		return fmt.Sprintf("http://console.dashborg.localdev:8080/acc/%s/%s/%s", accId, zoneName, panelName)
	}
	return fmt.Sprintf("https://console.dashborg.net/acc/%s/%s/%s", accId, zoneName, panelName)
}

func (req *PanelRequest) appendRR(rrAction *dashproto.RRAction) {
	req.Lock.Lock()
	defer req.Lock.Unlock()
	req.RRActions = append(req.RRActions, rrAction)
}

// SetData is used to return data to the client.  Will replace the contents of path with data.
func (req *PanelRequest) SetData(path string, data interface{}) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call SetData(), path=%s, PanelRequest is already done", path)
	}
	jsonData, err := marshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SetData, path:%s, err:%v\n", path, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "setdata",
		Selector:   path,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
}

// SetHtml returns html to be rendered by the client.  Currently only valid for "/" handler requests.
func (req *PanelRequest) SetHtml(html string) error {
	ts := dashutil.Ts()
	htmlAction := &dashproto.RRAction{
		Ts:         ts,
		ActionType: "html",
		Html:       html,
	}
	req.appendRR(htmlAction)
	return nil
}

// Convience wrapper over SetHtml that returns the contents of a file.
func (req *PanelRequest) SetHtmlFromFile(fileName string) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer fd.Close()
	htmlBytes, err := ioutil.ReadAll(fd)
	if err != nil {
		return err
	}
	return req.SetHtml(string(htmlBytes))
}

func (req *PanelRequest) isRootReq() bool {
	return req.RequestType == "handler" && req.PanelName != "" && req.Path == "/"
}

type authAtom struct {
	Scope string      `json:"scope"`
	Type  string      `json:"type"`
	Auto  bool        `json:"auto,omitempty"`
	Ts    int64       `json:"ts,omitempty"`
	Id    string      `json:"id,omitempty"`
	Role  string      `json:"role"`
	Data  interface{} `json:"data,omitempty"`
}

type challengeField struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	Type  string `json:"type"`
}

type authChallenge struct {
	AllowedAuth      string           `json:"allowedauth"` // challenge,dashborg
	ChallengeMessage string           `json:"challengemessage"`
	ChallengeError   string           `json:"challengeerror"`
	ChallengeFields  []challengeField `json:"challengefields"`
}

func (req *PanelRequest) appendPanelAuthRRAction(authType string, role string) {
	data := authAtom{
		Type: authType,
		Auto: true,
		Ts:   dashutil.Ts() + (24 * 60 * 60 * 1000),
		Role: role,
	}
	jsonData, _ := json.Marshal(data)
	rr := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "panelauth",
		JsonData:   string(jsonData),
	}
	req.appendRR(rr)
}

func (req *PanelRequest) appendPanelAuthChallenge(ch authChallenge) {
	challengeJson, _ := json.Marshal(ch)
	req.appendRR(&dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "panelauthchallenge",
		JsonData:   string(challengeJson),
	})
	return
}

func (req *PanelRequest) getRawAuthData() []*authAtom {
	if req.AuthData == nil {
		return nil
	}
	var rawAuth []*authAtom
	err := mapstructure.Decode(req.AuthData, &rawAuth)
	if err != nil {
		return nil
	}
	return rawAuth
}

func (req *PanelRequest) isAuthenticated() bool {
	rawAuth := req.getRawAuthData()
	return len(rawAuth) > 0
}

func (req *PanelRequest) hasPanelAuth(authType string) {
}

// call this function in your root handler to mark this panel as not requiring authentication
func (req *PanelRequest) NoAuth() {
	req.AuthImpl = true
	if !req.isAuthenticated() {
		req.appendPanelAuthRRAction("noauth", "user")
	}
}

type challengeData struct {
	ChallengeData map[string]string `json:"challengedata"`
}

// PasswordAuth sets a password to access this panel.  Note that password auth
//   also allows dashborg auth to access this panel.
func (req *PanelRequest) PasswordAuth(pw string) (bool, error) {
	req.AuthImpl = true
	if req.isAuthenticated() {
		return true, nil
	}
	// check challenge
	var challengeData challengeData
	err := mapstructure.Decode(req.Data, &challengeData)
	if err == nil && challengeData.ChallengeData["password"] == pw {
		req.appendPanelAuthRRAction("password", "user")
		return true, nil
	}
	// send challenge
	ch := authChallenge{
		AllowedAuth: "challenge,dashborg",
		ChallengeFields: []challengeField{challengeField{
			Label: "Panel Password",
			Name:  "password",
			Type:  "password",
		}},
	}
	if challengeData.ChallengeData["submitted"] == "1" {
		if challengeData.ChallengeData["password"] == "" {
			ch.ChallengeError = "Password cannot be blank"
		} else {
			ch.ChallengeError = "Invalid password"
		}
	}
	req.appendPanelAuthChallenge(ch)
	return false, fmt.Errorf("Not Authorized | Sending Password Challenge")
}

// DashborgAuth requires a valid dashborg user account to access this panel.
func (req *PanelRequest) DashborgAuth() (bool, error) {
	req.AuthImpl = true
	if req.isAuthenticated() {
		return true, nil
	}
	// send challenge
	ch := authChallenge{
		AllowedAuth: "dashborg",
	}
	req.appendPanelAuthChallenge(ch)
	return false, fmt.Errorf("Not Authorized | Dashborg Auth")
}

// Call from a handler to force the client to invalidate and re-pull data that matches path.
//   Path is a regular expression.
func (req *PanelRequest) InvalidateData(path string) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call InvalidateData(), path=%s, PanelRequest is already done", path)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "invalidate",
		Selector:   path,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *PanelRequest) sendEvent(selector string, eventType string, data interface{}) error {
	if req.IsDone {
		return fmt.Errorf("Cannot call SendEvent(), selector=%s, event=%s, PanelRequest is already done", selector, eventType)
	}
	jsonData, err := marshalJson(data)
	if err != nil {
		return fmt.Errorf("Error marshaling json for SendEvent, selector:%s, event:%s, err:%v\n", selector, eventType, err)
	}
	rrAction := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "event",
		Selector:   selector,
		EventType:  eventType,
		JsonData:   jsonData,
	}
	req.appendRR(rrAction)
	return nil
}

func (req *PanelRequest) flush() error {
	if req.IsDone {
		return fmt.Errorf("Cannot Flush(), PanelRequest is already done")
	}
	return globalClient.sendRequestResponse(req, false)
}

// Done() ends a request and sends the results back to the client.  It is automatically called after
//   a handler/data-handler is run.  Only needs to be called explicitly if you'd like to return
//   your result earlier.
func (req *PanelRequest) Done() error {
	if req.IsDone {
		return nil
	}
	if !req.AuthImpl && req.isRootReq() {
		req.NoAuth()
	}
	err := globalClient.sendRequestResponse(req, true)
	if err != nil {
		logV("Dashborg ERROR sending handler response: %v\n", err)
	}
	return err
}

// RegisterPanelHandler registers a panel handler.  All panels require a root "/" handler.
func RegisterPanelHandler(panelName string, path string, handlerFn func(*PanelRequest) error) {
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "handler",
		Path:        path,
	}
	hfn := func(req *PanelRequest) (interface{}, error) {
		err := handlerFn(req)
		return nil, err
	}
	globalClient.registerHandler(hkey, hfn)
	if path == "/" {
		log.Printf("Dashborg Panel Link [%s]: %s\n", panelName, panelLink(panelName))
	}
}

// RegisterDataHandler registers a data handler.
func RegisterDataHandler(panelName string, path string, handlerFn func(*PanelRequest) (interface{}, error)) {
	hkey := &dashproto.HandlerKey{
		PanelName:   panelName,
		HandlerType: "data",
		Path:        path,
	}
	globalClient.registerHandler(hkey, handlerFn)
}

func logV(fmtStr string, args ...interface{}) {
	if globalClient != nil && globalClient.Config.Verbose {
		log.Printf(fmtStr, args...)
	}
}
