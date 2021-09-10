package dash

import (
	"time"
)

const MaxAuthExp = 24 * time.Hour
const AuthScopeZone = "zone"

type AuthAtom struct {
	Scope    string                 `json:"scope"` // scope of this atom acc, zone, app
	Type     string                 `json:"type"`  // auth type (password, noauth, dashborg, deauth, or user-defined)
	Ts       int64                  `json:"ts"`    // expiration Ts (ms) of this auth atom
	RoleList []string               `json:"role"`
	Id       string                 `json:"id,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// Returns AuthAtom role.  If AuthAtom is nil, returns "public"
func (aa *AuthAtom) HasRole(role string) bool {
	if aa == nil {
		return false
	}
	for _, checkRole := range aa.RoleList {
		if checkRole == role {
			return true
		}
	}
	return false
}

func (aa *AuthAtom) IsSuper() bool {
	return aa.HasRole(RoleSuper)
}

func (aa *AuthAtom) GetRoleList() []string {
	if aa == nil {
		return nil
	}
	return aa.RoleList
}
