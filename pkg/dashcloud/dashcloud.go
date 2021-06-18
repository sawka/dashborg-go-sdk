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

func MakeClient(config *dash.Config) (Container, error) {
	config.SetupForProcClient()
	container := makeCloudClient(config)
	container.startClient()
	return container, nil
}
