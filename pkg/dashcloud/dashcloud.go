package dashcloud

import (
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

type Container interface {
	ConnectApp(app dash.AppRuntime) error
	ReflectZone() (*dash.ReflectZoneType, error)
	BackendPush(appName string, path string, data interface{}) error
	CallDataHandler(appName string, path string, data interface{}) (interface{}, error)
	StartBareStream(panelName string, streamOpts dash.StreamOpts) (*dash.PanelRequest, error)
}

type containerImpl struct {
	Config      *dash.Config
	CloudClient *dashCloudClient
}

func (c *containerImpl) ConnectApp(app dash.AppRuntime) error {
	return c.CloudClient.connectApp(app)
}

func (c *containerImpl) ReflectZone() (*dash.ReflectZoneType, error) {
	return c.CloudClient.ReflectZone()
}

func (c *containerImpl) BackendPush(appName string, path string, data interface{}) error {
	return c.CloudClient.backendPush(appName, path)
}

func (c *containerImpl) CallDataHandler(appName string, path string, data interface{}) (interface{}, error) {
	return c.CloudClient.callDataHandler(appName, path, data)
}

func (c *containerImpl) StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.PanelRequest, error) {
	return c.CloudClient.startBareStream(appName, streamOpts)
}

func StartClient(config *dash.Config) (Container, error) {
	config.SetupForProcClient()
	c := &containerImpl{Config: config}
	c.CloudClient = makeCloudClient(config)
	c.CloudClient.startClient()
	return c, nil
}
