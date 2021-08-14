package dash

import (
	"time"
)

const MaxAuthExp = 24 * time.Hour

const (
	AuthScopeApp  = "app"
	AuthScopeZone = "zone"
)

type AuthAtom struct {
	Scope string                 `json:"scope"` // scope of this atom acc, zone, app
	Type  string                 `json:"type"`  // auth type (password, noauth, dashborg, deauth, or user-defined)
	Ts    int64                  `json:"ts"`    // expiration Ts (ms) of this auth atom
	Role  string                 `json:"role"`
	Id    string                 `json:"id,omitempty"`
	Data  map[string]interface{} `json:"data,omitempty"`
}

// Returns AuthAtom role.  If AuthAtom is nil, returns "public"
func (aa *AuthAtom) GetRole() string {
	if aa == nil {
		return ""
	}
	return aa.Role
}
