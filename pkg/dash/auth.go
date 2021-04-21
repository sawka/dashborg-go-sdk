package dash

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dgrijalva/jwt-go"
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
	AllowedAuth string `json:"allowedauth"` // "dashborg", "challenge", "message", "simplejwt", "removeparam"

	// These fields only apply when AllowedAuth = "challenge"
	ChallengeMessage string           `json:"challengemessage,omitempty"` // message to show user for this auth
	ChallengeError   string           `json:"challengeerror,omitempty"`   // error message to show
	ChallengeFields  []challengeField `json:"challengefields,omitempty"`  // array of challenge fields

	MessageTitle string `json:"messagetitle,omitempty"`
	Message      string `json:"message,omitempty"`

	RemoveParam string `json:"removeparam,omitempty"` // for JWT auth, remove jwt param from browser URL string
}

type challengeData struct {
	ChallengeData map[string]string `json:"challengedata"`
}

type AuthNone struct{}

type AuthDashborg struct{}

type AuthPassword struct {
	Password string
}

// Simple JWT authentication token.
// Will validate "iss" against Issuer, "aud" optionally against Audience, and "iat" and "exp" for validity.
// Then will set an Dashborg authAtom with type = "simplejwt", id = the "sub" claim.
// Role will be set to "user" or to the nonstandard "role" claim, if given.
type AuthSimpleJwt struct {
	Issuer     string // must match "iss" claim
	ParamName  string // URL parameter to read the JWT token from
	Audience   string // optional, if present will be validated against the "aud" claim
	SigningKey []byte // secret key, HMAC signed with HS256
}

// Creates a JWT token that is compatible with this auth method.  If validFor is set to 0, it will
// default to 15 minutes.  This is how long the JWT token can be used to authenticate a user to
// the panel.  Once authenticated, by default, a user will stay authenticated for 24-hours regarless
// of the token's expiration time.
func (auth AuthSimpleJwt) MakeJWT(id string, validFor time.Duration) (string, error) {
	if auth.Issuer == "" {
		return "", errors.New("SimpleJWT must have an Issuer")
	}
	if len(auth.SigningKey) == 0 {
		return "", errors.New("SimpleJWT must have a SigningKey")
	}
	if id == "" {
		return "", errors.New("SimpleJWT requires an 'id'")
	}
	if validFor == 0 {
		validFor = 15 * time.Minute
	}
	claims := jwt.MapClaims{}
	claims["iss"] = auth.Issuer
	claims["exp"] = time.Now().Add(validFor).Unix()
	claims["iat"] = time.Now().Add(-5 * time.Second).Unix() // skew
	if auth.Audience != "" {
		claims["aud"] = auth.Audience
	}
	claims["sub"] = id
	token := jwt.NewWithClaims(jwt.GetSigningMethod("HS256"), claims)
	jwtStr, err := token.SignedString(auth.SigningKey)
	if err != nil {
		return "", err
	}
	return jwtStr, nil
}

func (auth AuthSimpleJwt) MustMakeJWT(id string, validFor time.Duration) string {
	jwtStr, err := auth.MakeJWT(id, validFor)
	if err != nil {
		panic(err)
	}
	return jwtStr
}

type AuthSimpleLoginResponse struct {
	LoginOk bool
	Id      string
	Role    string
}

type AuthSimpleLogin struct {
	// returns loginOk, id, error
	CheckFn func(user string, password string) (*AuthSimpleLoginResponse, error)
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

func (auth AuthSimpleJwt) checkAuth(req *PanelRequest) (bool, error) {
	ok, err := auth.checkAuthInternal(req)
	if err != nil {
		// send a message
		ac := authChallenge{
			AllowedAuth:  "message",
			RemoveParam:  auth.ParamName,
			MessageTitle: "External Sign In Error",
			Message:      err.Error(),
		}
		req.appendPanelAuthChallenge(ac)
		return false, err
	}
	if !ok {
		return false, nil
	}

	// must remove param on success
	ac := authChallenge{
		AllowedAuth: "removeparam",
		RemoveParam: auth.ParamName,
	}
	req.appendPanelAuthChallenge(ac)
	return true, nil
}

func (auth AuthSimpleJwt) checkAuthInternal(req *PanelRequest) (bool, error) {
	type panelState struct {
		UrlParams map[string]string `json:"urlparams"`
	}
	var pstate panelState
	err := mapstructure.Decode(req.PanelState, &pstate)
	if err != nil {
		return false, nil
	}
	jwtParam := pstate.UrlParams[auth.ParamName]
	if jwtParam == "" {
		return false, nil
	}
	type simpleJwtClaims struct {
		Role string `json:"role"`
		jwt.StandardClaims
	}
	var claims simpleJwtClaims
	_, err = jwt.ParseWithClaims(jwtParam, &claims, func(t *jwt.Token) (interface{}, error) {
		return auth.SigningKey, nil
	})
	if err != nil {
		return false, fmt.Errorf("Error Parsing JWT token '%s': %w", auth.ParamName, err)
	}
	err = claims.Valid()
	if err != nil {
		return false, fmt.Errorf("Invalid JWT token '%s': %w", auth.ParamName, err)
	}
	if claims.Issuer != auth.Issuer {
		return false, fmt.Errorf("Wrong issuer for JWT token '%s' got[%s] expected[%s]", auth.ParamName, claims.Issuer, auth.Issuer)
	}
	if auth.Audience != "" && claims.Audience != auth.Audience {
		return false, fmt.Errorf("Wrong audience for JWT token '%s' got[%s] expected[%s]", auth.ParamName, claims.Audience, auth.Audience)
	}
	role := "user"
	if claims.Role != "" {
		if !dashutil.IsRoleValid(claims.Role) {
			return false, fmt.Errorf("JWT token '%s' has invalid role", auth.ParamName)
		}
		role = claims.Role
	}
	if claims.Subject == "" {
		return false, fmt.Errorf("JWT token '%s' does not contain a subject", auth.ParamName)
	}
	req.setAuthData(authAtom{
		Type: "simplejwt",
		Id:   claims.Subject,
		Role: role,
	})
	return true, nil
}

func (aup AuthSimpleLogin) checkAuth(req *PanelRequest) (bool, error) {
	var challengeData challengeData
	err := mapstructure.Decode(req.Data, &challengeData)
	if err != nil {
		return false, nil
	}
	user, userOk := challengeData.ChallengeData["user"]
	pw, passwordOk := challengeData.ChallengeData["password"]
	if !userOk || !passwordOk {
		return false, nil
	}
	resp, err := aup.CheckFn(user, pw)
	if err != nil || resp == nil {
		return false, err
	}
	if !resp.LoginOk {
		return false, nil
	}
	role := "user"
	if resp.Role != "" {
		role = resp.Role
	}
	if !dashutil.IsRoleValid(role) {
		return false, errors.New("Invalid role for user, cannot authenticate")
	}
	req.setAuthData(authAtom{
		Type: "login",
		Id:   resp.Id,
		Role: role,
	})
	return true, nil
}

func (AuthDashborg) returnChallenge(req *PanelRequest) *authChallenge {
	return &authChallenge{
		AllowedAuth: "dashborg",
	}
}

func (auth AuthSimpleJwt) returnChallenge(req *PanelRequest) *authChallenge {
	return &authChallenge{
		AllowedAuth: "simplejwt",
		RemoveParam: auth.ParamName,
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

func (aup AuthSimpleLogin) returnChallenge(req *PanelRequest) *authChallenge {
	var challengeData challengeData
	mapstructure.Decode(req.Data, &challengeData) // don't check error
	ch := &authChallenge{
		AllowedAuth: "challenge",
		ChallengeFields: []challengeField{
			challengeField{
				Label: "User",
				Name:  "user",
				Type:  "text",
			},
			challengeField{
				Label: "Password",
				Name:  "password",
				Type:  "password",
			},
		},
	}
	if challengeData.ChallengeData["submitted"] == "1" {
		user := challengeData.ChallengeData["user"]
		pw := challengeData.ChallengeData["password"]
		resp, err := aup.CheckFn(user, pw)
		if err != nil {
			ch.ChallengeError = err.Error()
		} else if resp == nil {
			ch.ChallengeError = "Invalid User/Password"
		} else if resp.LoginOk && resp.Role != "" && !dashutil.IsRoleValid(resp.Role) {
			ch.ChallengeError = "Invalid role for user, cannot authenticate"
		} else {
			ch.ChallengeError = "Invalid User/Password"
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
