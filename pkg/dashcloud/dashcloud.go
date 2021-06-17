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
	ConnectApp(app dash.AppRuntime) error
	ReflectZone() (*dash.ReflectZoneType, error)
	BackendPush(appName string, path string, data interface{}) error
	CallDataHandler(appName string, path string, data interface{}) (interface{}, error)
}

type containerImpl struct {
	Config Config
}

func (c *containerImpl) ConnectApp(app dash.AppRuntime) error {
	err := dash.ConnectApp(app)
	return err
}

func (c *containerImpl) ReflectZone() (*dash.ReflectZoneType, error) {
	return dash.ReflectZone()
}

func (c *containerImpl) BackendPush(appName string, path string, data interface{}) error {
	return dash.BackendPush(appName, path)
}

func (c *containerImpl) CallDataHandler(appName string, path string, data interface{}) (interface{}, error) {
	return dash.CallDataHandler(appName, path, data)
}

func MakeClient(config *dash.Config) (Container, error) {
	dash.StartProcClient(config)
	return &containerImpl{}, nil
}
