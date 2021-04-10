package dash

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const MAX_AUTH_EXP = 24 * time.Hour

type authAtom struct {
	Scope string      `json:"scope"`        // scope of this atom (panel:[zone]:[panel], zone:[zone], or acc)
	Type  string      `json:"type"`         // auth type (password, noauth, dashborg, deauth, or user-defined)
	Ts    int64       `json:"ts,omitempty"` // expiration Ts (ms) of this auth atom
	Id    string      `json:"id,omitempty"`
	Role  string      `json:"role"`
	Data  interface{} `json:"data,omitempty"`
}

type challengeField struct {
	Label string `json:"label"` // label for challenge field in UI
	Name  string `json:"name"`  // key for field (returned in challengedata)
	Type  string `json:"type"`  // "password" or "text"
}

type authChallenge struct {
	AllowedAuth string `json:"allowedauth"` // "dashborg" or "challenge" or "message"

	// These fields only apply when AllowedAuth = "challenge"
	ChallengeMessage string           `json:"challengemessage"` // message to show user for this auth
	ChallengeError   string           `json:"challengeerror"`   // error message to show
	ChallengeFields  []challengeField `json:"challengefields"`  // array of challenge fields

	MessageTitle string `json:"messagetitle"`
	Message      string `json:"message"`
}

type challengeData struct {
	ChallengeData map[string]string `json:"challengedata"`
}

type AuthNone struct{}

type AuthDashborg struct{}

type AuthPassword struct {
	Password string
}

type AuthSimpleJwt struct {
	Key string
}

type AllowedAuth interface {
	checkAuth(*PanelRequest) (bool, error) // should call setAuthData if returns true
}

type challengeAuth interface {
	returnChallenge(*PanelRequest) *authChallenge //
}

func (AuthNone) checkAuth(req *PanelRequest) (bool, error) {
	req.setAuthData(authAtom{
		Type: "noauth",
		Role: "user",
	})
	return true, nil
}

func (AuthDashborg) checkAuth(req *PanelRequest) (bool, error) {
	return false, nil
}

func (auth AuthPassword) checkAuth(req *PanelRequest) (bool, error) {
	var challengeData challengeData
	err := mapstructure.Decode(req.Data, &challengeData)
	if err == nil && challengeData.ChallengeData["password"] == auth.Password {
		req.setAuthData(authAtom{
			Type: "password",
			Role: "user",
		})
		return true, nil
	}
	return false, nil
}

func (AuthDashborg) returnChallenge(req *PanelRequest) *authChallenge {
	return &authChallenge{
		AllowedAuth: "dashborg",
	}
}

func (auth AuthPassword) returnChallenge(req *PanelRequest) *authChallenge {
	var challengeData challengeData
	mapstructure.Decode(req.Data, &challengeData) // don't check error
	ch := &authChallenge{
		AllowedAuth: "challenge",
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
	return ch
}

func (req *PanelRequest) getAuthAtom(aType string) *authAtom {
	for _, aa := range req.AuthData {
		if aa.Type == aType {
			return aa
		}
	}
	return nil
}

func (req *PanelRequest) setAuthData(aa authAtom) {
	if aa.Scope == "" {
		aa.Scope = fmt.Sprintf("panel:%s:%s", globalClient.Config.ZoneName, req.PanelName)
	}
	if aa.Ts == 0 {
		aa.Ts = dashutil.Ts() + int64(MAX_AUTH_EXP/time.Millisecond)
	}
	if aa.Type == "" {
		panic(fmt.Sprintf("Dashborg Invalid AuthAtom, no Type specified"))
	}
	jsonAa, _ := json.Marshal(aa)
	rr := &dashproto.RRAction{
		Ts:         dashutil.Ts(),
		ActionType: "panelauth",
		JsonData:   string(jsonAa),
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

func (req *PanelRequest) IsAuthenticated() bool {
	if req.IsLocal {
		return true
	}
	rawAuth := req.getRawAuthData()
	return len(rawAuth) > 0
}

// If AllowedAuth impelementations return an error they will be logged into
// req.Info.  They will not stop the execution of the function since
// other auth methods might succeed.
func (req *PanelRequest) CheckAuth(allowedAuths ...AllowedAuth) bool {
	req.AuthImpl = true
	if req.IsAuthenticated() {
		return true
	}
	for _, aa := range allowedAuths {
		ok, err := aa.checkAuth(req)
		if err != nil {
			req.Info = append(req.Info, err.Error())
		}
		if ok {
			return true
		}
	}
	for _, aa := range allowedAuths {
		if chAuth, ok := aa.(challengeAuth); ok {
			ch := chAuth.returnChallenge(req)
			if ch != nil {
				req.appendPanelAuthChallenge(*ch)
			}
		}
	}
	return false
}
