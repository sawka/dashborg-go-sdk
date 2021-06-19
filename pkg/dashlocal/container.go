package dashlocal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

type ContainerConfig struct {
	Addr       string        // defaults to localhost:8082
	ShutdownCh chan struct{} // channel for shutting down server
	AccId      string
	ZoneName   string
	Env        string
	Verbose    bool
}

type Container interface {
	ConnectApp(app dash.AppRuntime) error
	StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.PanelRequest, error)
	BackendPush(appName string, path string, data interface{}) error
}

type containerImpl struct {
	Lock      *sync.Mutex
	Config    ContainerConfig
	App       dash.AppRuntime
	AppClient dash.AppClient
	RootHtml  string

	LocalClient *localClient
	LocalServer *localServer

	DynamicHtml   bool
	OnloadHandler string
}

func (c *containerImpl) getAppName() string {
	if c.App == nil {
		return "noapp"
	}
	return c.App.GetAppName()
}

func (c *containerImpl) getClientVersion() string {
	if c.App == nil {
		return "noclient-0.0.0"
	}
	return c.App.GetClientVersion()
}

func (c *ContainerConfig) setDefaults() {
	if c.Addr == "" {
		c.Addr = "localhost:8082"
	}
	if c.ZoneName == "" {
		c.ZoneName = "default"
	}
	if c.Env == "" {
		c.Env = "prod"
	}
}

func optJson(opt dash.AppOption) string {
	rtn, _ := dashutil.MarshalJson(opt)
	return rtn
}

func (c *containerImpl) processHtmlOption(optData interface{}) error {
	var htmlOpt dash.GenericAppOption
	err := mapstructure.Decode(optData, &htmlOpt)
	if err != nil {
		return err
	}
	if htmlOpt.Type == "dynamic" {
		c.DynamicHtml = true
		return nil
	} else {
		return fmt.Errorf("Invalid 'html' option type: %s\n", dashutil.MarshalJsonNoError(optData))
	}
}

func (c *containerImpl) processOnloadHandlerOption(optData interface{}) error {
	var loadOpt dash.GenericAppOption
	err := mapstructure.Decode(optData, &loadOpt)
	if err != nil {
		return err
	}
	c.OnloadHandler = loadOpt.Path
	return nil
}

func (c *containerImpl) ConnectApp(app dash.AppRuntime) error {
	if c.App != nil {
		log.Printf("Dashborg LocalContainer cannot connect a second app to local container")
		return fmt.Errorf("Cannot connect a second app to local container")
	}
	appConfig := app.AppConfig()
	for optName, opt := range appConfig.Options {
		var optErr error
		switch optName {
		case "html":
			optErr = c.processHtmlOption(opt)

		case "onloadhandler":
			optErr = c.processOnloadHandlerOption(opt)

		default:
			log.Printf("Dashborg LocalContainer WARNING opt[%s]: unsupported option\n", optName)
		}
		if optErr != nil {
			log.Printf("Dashborg LocalContainer ERROR opt[%s]: %v\n", optName, optErr)
			return optErr
		}
	}
	connId := &atomic.Value{}
	connId.Store(uuid.New().String())
	appClient := dash.MakeAppClient(c, app, c.LocalClient, &dash.Config{}, connId)
	c.App = app
	c.AppClient = appClient
	c.LocalClient.SetAppClient(appClient)
	log.Printf("Connected app[%s] to Local Container @ %s\n", c.App.GetAppName(), c.Config.Addr)
	return nil
}

func (c *containerImpl) StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.PanelRequest, error) {
	c.Lock.Lock()
	app := c.App
	appClient := c.AppClient
	c.Lock.Unlock()
	if app == nil || app.GetAppName() != appName {
		return nil, fmt.Errorf("No active app[%s] found for StartBareStream", appName)
	}
	req, _, err := appClient.StartStream(appName, streamOpts, "")
	return req, err
}

func (c *containerImpl) BackendPush(panelName string, path string, data interface{}) error {
	m := &dashproto.BackendPushMessage{
		Ts:        dashutil.Ts(),
		PanelName: panelName,
		Path:      path,
	}
	resp, err := c.LocalClient.BackendPush(context.Background(), m)
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if !resp.Success {
		return errors.New("Error calling BackendPush()")
	}
	return nil
}

func MakeContainer(config *ContainerConfig) (Container, error) {
	if config == nil {
		config = &ContainerConfig{}
	}
	config.setDefaults()
	pcConfig := &dash.Config{
		ZoneName:    "default",
		LocalServer: true,
		Env:         config.Env,
		Verbose:     config.Verbose,
	}
	pcConfig.SetupForProcClient()
	config.AccId = pcConfig.AccId
	config.ZoneName = pcConfig.ZoneName
	container := &containerImpl{Lock: &sync.Mutex{}, Config: *config}
	lsConfig := &localServerConfig{
		Env:     config.Env,
		Addr:    config.Addr,
		Verbose: config.Verbose,
	}
	dbService, err := makeLocalClient(lsConfig, container)
	if err != nil {
		return nil, err
	}
	container.LocalClient = dbService
	pcConfig.LocalClient = dbService
	localServer, err := makeLocalServer(lsConfig, dbService, container)
	if err != nil {
		return nil, err
	}
	container.LocalServer = localServer
	go func() {
		serveErr := container.LocalServer.listenAndServe()
		if serveErr != nil {
			log.Printf("Dashborg ERROR starting local server: %v\n", serveErr)
		}
	}()
	return container, nil
}
