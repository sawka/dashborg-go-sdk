package dashcloud

import (
	"fmt"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

func MakeClient(config *Config) (*DashCloudClient, error) {
	config.setDefaultsAndLoadKeys()
	container := makeCloudClient(config)
	err := container.startClient()
	if err != nil {
		return nil, err
	}
	return container, nil
}

type ReflectProcType struct {
	StartTs   int64             `json:"startts"`
	ProcName  string            `json:"procname"`
	ProcTags  map[string]string `json:"proctags"`
	ProcRunId string            `json:"procrunid"`
}

type JWTOpts struct {
	NoJwt    bool
	ValidFor time.Duration
	UserId   string
	Role     string
}

func (jwtOpts *JWTOpts) ValidateAndSetDefaults() error {
	if jwtOpts.NoJwt {
		return nil
	}
	if jwtOpts.ValidFor < 0 {
		return dasherr.ValidateErr(fmt.Errorf("Invalid ValidTime (negative)"))
	}
	if jwtOpts.ValidFor == 0 {
		jwtOpts.ValidFor = DefaultJwtValidFor
	}
	if jwtOpts.Role == "" {
		jwtOpts.Role = DefaultJwtRole
	}
	if jwtOpts.UserId == "" {
		jwtOpts.UserId = DefaultJwtUserId
	}
	if !dashutil.IsRoleListValid(jwtOpts.Role) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid Role"))
	}
	if !dashutil.IsUserIdValid(jwtOpts.UserId) {
		return dasherr.ValidateErr(fmt.Errorf("Invalid UserId"))
	}
	if jwtOpts.ValidFor > 24*time.Hour {
		return dasherr.ValidateErr(fmt.Errorf("Maximum validFor for JWT tokens is 24-hours"))
	}
	return nil
}
