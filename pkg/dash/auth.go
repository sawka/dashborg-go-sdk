package dash

import (
	"encoding/json"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const MAX_AUTH_EXP = 24 * time.Hour

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
	AllowedAuth      string           `json:"allowedauth"`
	ChallengeMessage string           `json:"challengemessage"`
	ChallengeError   string           `json:"challengeerror"`
	ChallengeFields  []challengeField `json:"challengefields"`
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
	checkAuth(*PanelRequest) (bool, error) // calls setAuthData if returns true
}

type challengeAuth interface {
	returnChallenge(*PanelRequest) *authChallenge //
}

func (AuthNone) checkAuth(req *PanelRequest) (bool, error) {
	req.setAuthData("noauth", "user", MAX_AUTH_EXP, nil)
	return true, nil
}

func (AuthDashborg) checkAuth(req *PanelRequest) (bool, error) {
	return false, nil
}

func (auth AuthPassword) checkAuth(req *PanelRequest) (bool, error) {
	var challengeData challengeData
	err := mapstructure.Decode(req.Data, &challengeData)
	if err == nil && challengeData.ChallengeData["password"] == auth.Password {
		req.setAuthData("password", "user", MAX_AUTH_EXP, nil)
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

func (req *PanelRequest) setAuthData(authType string, role string, exp time.Duration, data interface{}) {
	if exp > MAX_AUTH_EXP {
		exp = MAX_AUTH_EXP
	}
	aa := authAtom{
		Type: authType,
		Auto: true,
		Ts:   dashutil.Ts() + int64(exp/time.Millisecond),
		Role: role,
		Data: data,
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
