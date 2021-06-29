package dashlocal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

// Configuration structure for the local container.  If values are not set, they are read
// from the environment variables specified in all-caps below, or set to the specified defaults.
type Config struct {
	// DASHBORG_LOCALADDR, defaults to "localhost:8082"
	Addr string

	// Channel for shutting down the server
	ShutdownCh chan struct{}

	// DASHBORG_VERBOSE, set to true for extra debugging information
	Verbose bool

	// The local container does not support auth, but apps still require it.
	// This will set the default auth role that the container will provide to
	// all apps it hosts.  Setting this to "none" will disable the automatic auth
	// manual auth (and allowed role of "public").  Defaults to "user".
	DefaultAuthRole string

	// For internal testing, DASHBORG_ENV
	Env string // defaults to "prod"
}

type Container interface {
	// Call to connect an app to this container.  The local container only supports one app,
	// trying to connect a 2nd app will return an error.
	ConnectApp(app dash.AppRuntime) error

	// Returns an error when the container has be shutdown (or there was an http Serve error).
	// a normal shutdown will return http.ErrServerClosed.  If the http server is still
	// running, will return nil.
	ServeErr() error

	// Dashborg method for starting a bare stream that is not connected to a request or frontend.
	StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.Request, error)

	// Dashborg method for forcing all frontend clients to make the specified handler call.
	BackendPush(appName string, path string, data interface{}) error
}

type containerImpl struct {
	Lock          *sync.Mutex
	ServeErrValue *atomic.Value
	Config        Config
	App           dash.AppRuntime
	AppClient     dash.AppClient
	RootHtml      string

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

func (c *containerImpl) ServeErr() error {
	errIf := c.ServeErrValue.Load()
	if errIf != nil {
		return errIf.(error)
	}
	return nil
}

func (c *containerImpl) getClientVersion() string {
	if c.App == nil {
		return "noapp-0.0.0"
	}
	return c.App.GetClientVersion()
}

func (c *Config) setDefaults() {
	c.Addr = dashutil.DefaultString(c.Addr, os.Getenv("DASHBORG_LOCALADDR"), "localhost:8082")
	c.Env = dashutil.DefaultString(c.Env, os.Getenv("DASHBORG_ENV"), "prod")
	c.DefaultAuthRole = dashutil.DefaultString(c.DefaultAuthRole, "user")
	c.Verbose = dashutil.EnvOverride(c.Verbose, "DASHBORG_VERBOSE")
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
	if serveErr := c.ServeErr(); serveErr != nil {
		log.Printf("Dashborg LocalContainer ERROR cannot connect app, server shutdown: %v", serveErr)
		return fmt.Errorf("Cannot connect app, server shutdown: %w", serveErr)
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
	clientConfig := dash.AppClientConfig{
		PublicKey: nil,
		Verbose:   c.Config.Verbose,
	}
	appClient := dash.MakeAppClient(c, app, c.LocalClient, clientConfig, connId)
	c.App = app
	c.AppClient = appClient
	c.LocalClient.SetAppClient(appClient)
	log.Printf("Connected app[%s] to Local Container @ %s\n", c.App.GetAppName(), c.Config.Addr)
	return nil
}

func (c *containerImpl) StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.Request, error) {
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

// Creates a local container.  Config may be nil, in which case the defaults
// (or environment overrides) are used.
func MakeContainer(config *Config) (Container, error) {
	if config == nil {
		config = &Config{}
	}
	config.setDefaults()
	container := &containerImpl{Lock: &sync.Mutex{}, Config: *config, ServeErrValue: &atomic.Value{}}
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
	localServer, err := makeLocalServer(lsConfig, dbService, container)
	if err != nil {
		return nil, err
	}
	container.LocalServer = localServer

	// code here to attempt to catch an immediate error return from http.ListenAndServe (e.g. bind port in use).
	// we have to put a small sleep to yield the scheduler to the new go-routine.  note that there is a timing
	// issue here, but it is unavoidable.  this works for the 90% case.
	log.Printf("Dashborg Local Container starting at http://%s\n", config.Addr)
	go func() {
		serveErr := container.LocalServer.listenAndServe()
		if serveErr != nil {
			log.Printf("Dashborg ERROR starting local server: %v\n", serveErr)
		}
		container.ServeErrValue.Store(serveErr)
	}()
	time.Sleep(1 * time.Millisecond)
	return container, container.ServeErr()
}
