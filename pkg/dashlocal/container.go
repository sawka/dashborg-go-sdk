package dashlocal

import (
	"fmt"
	"log"
	"sync"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const CONTAINER_VERSION = "gocontainer-0.6.0"

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
}

type containerImpl struct {
	Lock     *sync.Mutex
	Config   ContainerConfig
	App      dash.AppRuntime
	RootHtml string

	LocalClient dashproto.DashborgServiceClient
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
		return fmt.Errorf("Cannot connect a second app to local container")
	}
	c.App = app
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
	dash.ConnectApp(c.App)
	log.Printf("Connected app[%s] to Local Container @ %s\n", c.App.GetAppName(), c.Config.Addr)
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
		Env:  config.Env,
		Addr: config.Addr,
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
	dash.StartProcClient(pcConfig)
	return container, nil
}
