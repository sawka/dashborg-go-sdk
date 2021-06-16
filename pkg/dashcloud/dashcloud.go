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

type Container interface {
	ConnectApp(app dash.App) error
}

type containerImpl struct {
	Config Config
}

func (c *containerImpl) ConnectApp(app dash.App) error {
	err := dash.ConnectApp(app)
	return err
}

func StartClient(config *dash.Config) (Container, error) {
	dash.StartProcClient(config)
	return &containerImpl{}, nil
}
