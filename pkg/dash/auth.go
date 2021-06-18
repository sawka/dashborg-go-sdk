package dash

import (
	"errors"
	"fmt"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const MaxAuthExp = 24 * time.Hour

const (
	AuthScopeApp  = "app"
	AuthScopeZone = "zone"
)

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

// Authenticate against a JWT constructed with account public/private keypair
type AuthAccountJwt struct {
	ParamName string // URL parameter to read the JWT token from (or blank for $state.dbrequest.embedauthtoken)
}

// Creates a JWT token that is compatible with this auth method.  If validFor is set to 0, it will
// default to 15 minutes.  This is how long the JWT token can be used to authenticate a user to
// the panel.  Once authenticated, by default, a user will stay authenticated for 24-hours regarless
// of the token's expiration time.
func (auth AuthSimpleJwt) MakeJWT(validFor time.Duration, id string) (string, error) {
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
	if id != "" {
		claims["sub"] = id
	}
	token := jwt.NewWithClaims(jwt.GetSigningMethod("HS256"), claims)
	jwtStr, err := token.SignedString(auth.SigningKey)
	if err != nil {
		return "", err
	}
	return jwtStr, nil
}

func (auth AuthSimpleJwt) MustMakeJWT(id string, validFor time.Duration) string {
	jwtStr, err := auth.MakeJWT(validFor, id)
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
	CheckFn func(user string, password string) (*AuthSimpleLoginResponse, error)
}

type challengeAuth interface {
	returnChallenge(*PanelRequest) *authChallenge //
}

func (AuthNone) checkAuth(req *PanelRequest) (bool, error) {
	req.setAuthData(AuthAtom{
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
		req.setAuthData(AuthAtom{
			Type: "password",
			Role: "user",
		})
		return true, nil
	}
	return false, nil
}

func (auth AuthAccountJwt) checkAuth(req *PanelRequest) (bool, error) {
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
	if auth.ParamName != "" {
		ac := authChallenge{
			AllowedAuth: "removeparam",
			RemoveParam: auth.ParamName,
		}
		req.appendPanelAuthChallenge(ac)
	}
	return true, nil
}

func (auth AuthAccountJwt) checkAuthInternal(req *PanelRequest) (bool, error) {
	type panelState struct {
		UrlParams map[string]string `json:"urlparams"`
		DbRequest map[string]string `json:"dbrequest"`
	}
	var pstate panelState
	err := mapstructure.Decode(req.PanelState, &pstate)
	if err != nil {
		return false, nil
	}
	var jwtParam string
	if auth.ParamName == "" {
		jwtParam = pstate.DbRequest["embedauthtoken"]
	} else {
		jwtParam = pstate.UrlParams[auth.ParamName]
	}
	if jwtParam == "" {
		return false, nil
	}
	type authJwtClaims struct {
		jwt.StandardClaims
		Role    string `json:"role"`
		DashAcc string `json:"dash-acc"`
	}
	cert, err := readCertInfo(req.appClient.Config.CertFileName)
	if err != nil {
		// strange given that the client is running.
		return false, fmt.Errorf("Error Validating JWT account token")
	}
	var claims authJwtClaims
	_, err = jwt.ParseWithClaims(jwtParam, &claims, func(t *jwt.Token) (interface{}, error) {
		return cert.PublicKey, nil
	})
	if err != nil {
		return false, fmt.Errorf("Error Parsing JWT account token: %w", err)
	}
	err = claims.Valid()
	if err != nil {
		return false, fmt.Errorf("Invalid JWT token: %w", err)
	}
	if claims.Audience != "dashborg-auth" {
		return false, fmt.Errorf("Invalid JWT token, audience must be 'dashborg-auth'")
	}
	role := "user"
	if claims.Role != "" {
		if !dashutil.IsRoleValid(claims.Role) {
			return false, fmt.Errorf("JWT account token has invalid role")
		}
		role = claims.Role
	}
	req.setAuthData(AuthAtom{
		Type: "accountjwt",
		Id:   claims.Subject,
		Role: role,
	})
	return true, nil
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
	req.setAuthData(AuthAtom{
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
	req.setAuthData(AuthAtom{
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

func (auth AuthAccountJwt) returnChallenge(req *PanelRequest) *authChallenge {
	return &authChallenge{
		AllowedAuth: "accountjwt",
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
