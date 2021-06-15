package dashcloud

import (
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

type Config struct {
	ProcName   string
	AccId      string
	AnonAcc    bool
	AutoKeygen bool
	ZoneName   string
}

type BindOptions struct {
}

type CloudContainer interface {
	ConnectApp(app dash.App, bindOpts *BindOptions) error
}

type containerImpl struct {
	Config Config
}

func (c *containerImpl) ConnectApp(app dash.App, bindOpts *BindOptions) error {
	err := dash.ConnectApp(app)
	return err
}

func StartClient(config *dash.Config) (CloudContainer, error) {
	dash.StartProcClient(config)
	return &containerImpl{}, nil
}
