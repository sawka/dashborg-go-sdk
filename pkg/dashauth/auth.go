package dashauth

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

type AuthNone struct{}

type AuthDashborg struct{}

type AuthPassword struct {
	Password string
	Role     string // defaults to "user"
}

// allows multiple passwords.  each password can be associated with a different role (defaults to "user").
type AuthMultiPassword struct {
	Passwords []AuthPassword
}

type AllowedAuth interface {
	CheckAuth(*dash.Request) (*dash.AuthAtom, error)
}

type ChallengeAuth interface {
	ReturnChallenge(*dash.Request) *AuthChallenge
}

type ChallengeField struct {
	Label string `json:"label"` // label for challenge field in UI
	Name  string `json:"name"`  // key for field (returned in challengedata)
	Type  string `json:"type"`  // "password" or "text"
}

type AuthChallenge struct {
	AllowedAuth string `json:"allowedauth"` // "dashborg", "challenge", "message", "simplejwt", "removeparam"

	ChallengeType string `json:"challengetype,omitempty"` // distinguish different challenge type forms

	// These fields only apply when AllowedAuth = "challenge"
	ChallengeMessage string           `json:"challengemessage,omitempty"` // message to show user for this auth
	ChallengeError   string           `json:"challengeerror,omitempty"`   // error message to show
	ChallengeFields  []ChallengeField `json:"challengefields,omitempty"`  // array of challenge fields

	MessageTitle string `json:"messagetitle,omitempty"`
	Message      string `json:"message,omitempty"`

	RemoveParam string `json:"removeparam,omitempty"` // for JWT auth, remove jwt param from browser URL string
}

type ChallengeData struct {
	ChallengeData map[string]string `json:"challengedata"`
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

func (AuthNone) CheckAuth(req *dash.Request) (*dash.AuthAtom, error) {
	return &dash.AuthAtom{Type: "noauth", Role: "user"}, nil
}

func (AuthDashborg) CheckAuth(req *dash.Request) (*dash.AuthAtom, error) {
	return nil, nil
}

func (auth AuthPassword) CheckAuth(req *dash.Request) (*dash.AuthAtom, error) {
	var challengeData ChallengeData
	err := req.BindData(&challengeData)
	if err == nil && challengeData.ChallengeData["password"] == auth.Password {
		role := auth.Role
		if role == "" {
			role = "user"
		}
		return &dash.AuthAtom{Type: "password", Role: role}, nil
	}
	return nil, nil
}

func (auth AuthMultiPassword) CheckAuth(req *dash.Request) (*dash.AuthAtom, error) {
	var challengeData ChallengeData
	err := req.BindData(&challengeData)
	if err != nil {
		return nil, nil
	}
	chPassword := challengeData.ChallengeData["password"]
	if chPassword == "" {
		return nil, nil
	}
	for _, pauth := range auth.Passwords {
		if chPassword == pauth.Password {
			role := pauth.Role
			if role == "" {
				role = "user"
			}
			return &dash.AuthAtom{Type: "password", Role: role}, nil
		}
	}
	return nil, nil
}

func (auth AuthAccountJwt) CheckAuth(req *dash.Request) (*dash.AuthAtom, error) {
	rex := dash.RequestEx{req}
	authAtom, err := auth.checkAuthInternal(req)
	if err != nil {
		// send a message
		ac := AuthChallenge{
			AllowedAuth:  "message",
			RemoveParam:  auth.ParamName,
			MessageTitle: "External Sign In Error",
			Message:      err.Error(),
		}
		rex.AppendAppAuthChallenge(ac)
		return nil, err
	}
	if authAtom == nil {
		return nil, nil
	}
	// must remove param on success
	if auth.ParamName != "" {
		ac := AuthChallenge{
			AllowedAuth: "removeparam",
			RemoveParam: auth.ParamName,
		}
		rex.AppendAppAuthChallenge(ac)
	}
	return authAtom, nil
}

func (auth AuthAccountJwt) checkAuthInternal(req *dash.Request) (*dash.AuthAtom, error) {
	type appState struct {
		UrlParams map[string]string `json:"urlparams"`
		DbRequest map[string]string `json:"dbrequest"`
	}
	var pstate appState
	err := req.BindAppState(&pstate)
	if err != nil {
		return nil, nil
	}
	var jwtParam string
	if auth.ParamName == "" {
		jwtParam = pstate.DbRequest["embedauthtoken"]
	} else {
		jwtParam = pstate.UrlParams[auth.ParamName]
	}
	if jwtParam == "" {
		return nil, nil
	}
	return CheckDashborgAccountJWT(jwtParam, nil)
}

func (auth AuthSimpleJwt) CheckAuth(req *dash.Request) (*dash.AuthAtom, error) {
	rex := dash.RequestEx{req}
	authAtom, err := auth.checkAuthInternal(req)
	if err != nil {
		// send a message
		ac := AuthChallenge{
			AllowedAuth:  "message",
			RemoveParam:  auth.ParamName,
			MessageTitle: "External Sign In Error",
			Message:      err.Error(),
		}
		rex.AppendAppAuthChallenge(ac)
		return nil, err
	}
	if authAtom == nil {
		return nil, nil
	}

	// must remove param on success
	ac := AuthChallenge{
		AllowedAuth: "removeparam",
		RemoveParam: auth.ParamName,
	}
	rex.AppendAppAuthChallenge(ac)
	return authAtom, nil
}

func (auth AuthSimpleJwt) checkAuthInternal(req *dash.Request) (*dash.AuthAtom, error) {
	type appState struct {
		UrlParams map[string]string `json:"urlparams"`
	}
	var pstate appState
	err := req.BindAppState(&pstate)
	if err != nil {
		return nil, nil
	}
	jwtParam := pstate.UrlParams[auth.ParamName]
	if jwtParam == "" {
		return nil, nil
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
		return nil, fmt.Errorf("Error Parsing JWT token '%s': %w", auth.ParamName, err)
	}
	err = claims.Valid()
	if err != nil {
		return nil, fmt.Errorf("Invalid JWT token '%s': %w", auth.ParamName, err)
	}
	if claims.Issuer != auth.Issuer {
		return nil, fmt.Errorf("Wrong issuer for JWT token '%s' got[%s] expected[%s]", auth.ParamName, claims.Issuer, auth.Issuer)
	}
	if auth.Audience != "" && claims.Audience != auth.Audience {
		return nil, fmt.Errorf("Wrong audience for JWT token '%s' got[%s] expected[%s]", auth.ParamName, claims.Audience, auth.Audience)
	}
	role := "user"
	if claims.Role != "" {
		if !dashutil.IsRoleValid(claims.Role) {
			return nil, fmt.Errorf("JWT token '%s' has invalid role", auth.ParamName)
		}
		role = claims.Role
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("JWT token '%s' does not contain a subject", auth.ParamName)
	}
	return &dash.AuthAtom{Type: "simplejwt", Id: claims.Subject, Role: role}, nil
}

func (aup AuthSimpleLogin) CheckAuth(req *dash.Request) (*dash.AuthAtom, error) {
	var challengeData ChallengeData
	err := req.BindData(&challengeData)
	if err != nil {
		return nil, nil
	}
	user, userOk := challengeData.ChallengeData["user"]
	pw, passwordOk := challengeData.ChallengeData["password"]
	if !userOk || !passwordOk {
		return nil, nil
	}
	if aup.CheckFn == nil {
		return nil, fmt.Errorf("No Login CheckFn")
	}
	resp, err := aup.CheckFn(user, pw)
	if err != nil || resp == nil {
		return nil, err
	}
	if !resp.LoginOk {
		return nil, nil
	}
	role := "user"
	if resp.Role != "" {
		role = resp.Role
	}
	if !dashutil.IsRoleValid(role) {
		return nil, errors.New("Invalid role for user, cannot authenticate")
	}
	return &dash.AuthAtom{Type: "login", Id: resp.Id, Role: role}, nil
}

func (AuthDashborg) ReturnChallenge(req *dash.Request) *AuthChallenge {
	return &AuthChallenge{
		AllowedAuth: "dashborg",
	}
}

func (auth AuthSimpleJwt) ReturnChallenge(req *dash.Request) *AuthChallenge {
	return &AuthChallenge{
		AllowedAuth: "simplejwt",
		RemoveParam: auth.ParamName,
	}
}

func (auth AuthAccountJwt) ReturnChallenge(req *dash.Request) *AuthChallenge {
	return &AuthChallenge{
		AllowedAuth: "accountjwt",
		RemoveParam: auth.ParamName,
	}
}

func (auth AuthPassword) ReturnChallenge(req *dash.Request) *AuthChallenge {
	var challengeData ChallengeData
	req.BindData(&challengeData) // don't check error
	ch := &AuthChallenge{
		AllowedAuth:   "challenge",
		ChallengeType: "password",
		ChallengeFields: []ChallengeField{ChallengeField{
			Label: "App Password",
			Name:  "password",
			Type:  "password",
		}},
	}
	if challengeData.ChallengeData["submitted_challengetype"] == "password" {
		chPassword := challengeData.ChallengeData["password"]
		if chPassword == "" {
			ch.ChallengeError = "Password cannot be blank"
		} else if chPassword != auth.Password {
			ch.ChallengeError = "Invalid password"
		}
	}
	return ch
}

func (auth AuthMultiPassword) anyPasswordMatch(password string) bool {
	if password == "" {
		return false
	}
	for _, pauth := range auth.Passwords {
		if pauth.Password == password {
			return true
		}
	}
	return false
}

func (auth AuthMultiPassword) ReturnChallenge(req *dash.Request) *AuthChallenge {
	var challengeData ChallengeData
	req.BindData(&challengeData) // don't check error
	ch := &AuthChallenge{
		AllowedAuth:   "challenge",
		ChallengeType: "password",
		ChallengeFields: []ChallengeField{ChallengeField{
			Label: "App Password",
			Name:  "password",
			Type:  "password",
		}},
	}
	if challengeData.ChallengeData["submitted_challengetype"] == "password" {
		chPassword := challengeData.ChallengeData["password"]
		if chPassword == "" {
			ch.ChallengeError = "Password cannot be blank"
		} else if !auth.anyPasswordMatch(chPassword) {
			ch.ChallengeError = "Invalid password"
		}
	}
	return ch
}

func (aup AuthSimpleLogin) ReturnChallenge(req *dash.Request) *AuthChallenge {
	var challengeData ChallengeData
	req.BindData(&challengeData) // don't check error
	ch := &AuthChallenge{
		AllowedAuth:   "challenge",
		ChallengeType: "login",
		ChallengeFields: []ChallengeField{
			ChallengeField{
				Label: "User",
				Name:  "user",
				Type:  "text",
			},
			ChallengeField{
				Label: "Password",
				Name:  "password",
				Type:  "password",
			},
		},
	}
	if challengeData.ChallengeData["submitted_challengetype"] == "login" {
		user := challengeData.ChallengeData["user"]
		pw := challengeData.ChallengeData["password"]
		if aup.CheckFn == nil {
			ch.ChallengeError = "No Login CheckFn"
		} else {
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
	}
	return ch
}

func MakeAuthHandler(authHandlers ...AllowedAuth) func(req *dash.Request) error {
	passwordCount := 0
	loginCount := 0
	for _, auth := range authHandlers {
		switch auth.(type) {
		case AuthPassword, AuthMultiPassword:
			passwordCount++

		case AuthSimpleLogin:
			loginCount++
		}
	}
	if passwordCount > 1 {
		log.Printf("Dashborg WARNING may only include one AuthPassword, AuthMultiPassword handler\n")
	}
	if loginCount > 1 {
		log.Printf("Dashborg WARNING may only include one AuthSimpleLogin handler\n")
	}
	return func(req *dash.Request) error {
		rex := dash.RequestEx{req}
		authOpt, ok := rex.AppRuntime().GetAppConfig().Options[dash.OptionAuth]
		if !ok || len(authOpt.AllowedRoles) == 0 {
			return fmt.Errorf("Cannot MakeAuthHandler for App without option:auth allowed roles")
		}
		if IsAllowedRole(req.AuthData(), authOpt.AllowedRoles) {
			return nil
		}
		for _, auth := range authHandlers {
			authAtom, err := auth.CheckAuth(req)
			if err != nil {
				rex.AppendInfoMessage(err.Error())
			}
			if authAtom != nil {
				if IsAllowedRole(authAtom, authOpt.AllowedRoles) {
					rex.SetAuthData(authAtom)
					return nil
				} else {
					ac := AuthChallenge{
						AllowedAuth:  "message",
						MessageTitle: "Not Authorized",
						Message:      fmt.Sprintf("Role '%s' is not Authorized for this App", authAtom.GetRole()),
					}
					rex.AppendAppAuthChallenge(ac)
				}
			}
		}
		for _, auth := range authHandlers {
			if chAuth, ok := auth.(ChallengeAuth); ok {
				ch := chAuth.ReturnChallenge(req)
				if ch != nil {
					rex.AppendAppAuthChallenge(*ch)
				}
			}
		}
		return nil
	}
}

func CheckDashborgAccountJWT(jwtParam string, publicKeys []interface{}) (*dash.AuthAtom, error) {
	if len(publicKeys) == 0 {
		return nil, fmt.Errorf("No public key provided")
	}
	type authJwtClaims struct {
		jwt.StandardClaims
		Role    string `json:"role"`
		DashAcc string `json:"dash-acc"`
	}
	var claims authJwtClaims
	var parseErr error
	for _, pk := range publicKeys {
		_, parseErr = jwt.ParseWithClaims(jwtParam, &claims, func(t *jwt.Token) (interface{}, error) {
			return pk, nil
		})
		if parseErr == nil {
			break
		}
	}
	if parseErr != nil {
		return nil, fmt.Errorf("Error Parsing JWT account token: %w", parseErr)
	}
	err := claims.Valid()
	if err != nil {
		return nil, fmt.Errorf("Invalid JWT token: %w", err)
	}
	if claims.Audience != "dashborg-auth" {
		return nil, fmt.Errorf("Invalid JWT token, audience must be 'dashborg-auth'")
	}
	role := "user"
	if claims.Role != "" {
		if !dashutil.IsRoleValid(claims.Role) {
			return nil, fmt.Errorf("JWT account token has invalid role")
		}
		role = claims.Role
	}
	return &dash.AuthAtom{Type: "accountjwt", Id: claims.Subject, Role: role}, nil
}

func IsAllowedRole(aa *dash.AuthAtom, allowedRoles []string) bool {
	role := aa.GetRole()
	if role == dash.RoleSuper {
		return true
	}
	for _, allowedRole := range allowedRoles {
		if allowedRole == "public" || role == allowedRole {
			return true
		}
	}
	return false
}
