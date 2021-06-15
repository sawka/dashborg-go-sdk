package dashlocal

import (
	"fmt"
	"log"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
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

type Container struct {
	Config   ContainerConfig
	App      dash.App
	BindOpts BindOptions
	RootHtml string

	DynamicHtml   bool
	OnloadHandler string
}

type BindOptions struct {
}

func (c *ContainerConfig) SetDefaults() {
	if c.Addr == "" {
		c.Addr = "localhost:8082"
	}
	if c.AccId == "" {
		c.AccId = "local-container"
	}
	if c.ZoneName == "" {
		c.ZoneName = "default"
	}
	if c.Env == "" {
		c.Env = "prod"
	}
}

func MakeContainer(config *ContainerConfig) (*Container, error) {
	if config == nil {
		config = &ContainerConfig{}
	}
	config.SetDefaults()
	return &Container{Config: *config}, nil
}

func optJson(opt dash.AppOption) string {
	rtn, _ := dashutil.MarshalJson(opt)
	return rtn
}

func (c *Container) processHtmlOption(optData interface{}) error {
	var htmlOpt dash.HtmlOption
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

func (c *Container) processOnloadHandlerOption(optData interface{}) error {
	var loadOpt dash.OnloadHandlerOption
	err := mapstructure.Decode(optData, &loadOpt)
	if err != nil {
		return err
	}
	c.OnloadHandler = loadOpt.Path
	return nil
}

func (c *Container) ConnectApp(app dash.App, bindOpts *BindOptions) error {
	if c.App != nil {
		return fmt.Errorf("Cannot connect a second app to local container")
	}
	if bindOpts == nil {
		bindOpts = &BindOptions{}
	}
	c.App = app
	c.BindOpts = *bindOpts
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
	fmt.Printf("container %#v\n", c)

	return nil
}

func (c *Container) StartContainer() error {
	if c.App == nil {
		panic("Must register an App to start local container")
	}
	config := &dash.Config{
		ZoneName:    "default",
		LocalServer: true,
		Env:         c.Config.Env,
		Verbose:     c.Config.Verbose,
	}
	config.SetupForProcClient()
	lsConfig := &Config{
		AccId:         config.AccId,
		ZoneName:      "default",
		PanelName:     c.App.GetAppName(),
		Env:           c.Config.Env,
		Addr:          c.Config.Addr,
		ClientVersion: c.App.GetClientVersion(),
		PanelOpts:     c.BindOpts,
		Container:     c,
	}
	dbService, err := MakeLocalClient(lsConfig)
	if err != nil {
		return err
	}
	config.LocalClient = dbService
	dash.StartProcClient(config)
	dash.ConnectApp(c.App)
	return nil
}
