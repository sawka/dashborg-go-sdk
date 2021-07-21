package dashcloud

import (
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

type Config struct {
	// DASHBORG_ACCID, set to force an AccountId (must match certificate).  If not set, AccountId is set from certificate file.
	// If AccId is given and AutoKeygen is true, and key/cert files are not found, Dashborg will create a new self-signed
	//     keypair using the AccId given.
	// If AccId is given, and the certificate does not match, this will cause a panic.
	AccId string

	// Set to true for unregistered accounts
	AnonAcc bool

	// DASHBORG_ZONE defaults to "default"
	ZoneName string

	// Process Name Attributes.  Only ProcName is required
	ProcName string // DASHBORG_PROCNAME (set from executable filename if not set)
	ProcTags map[string]string

	KeyFileName  string // DASHBORG_KEYFILE private key file (defaults to dashborg-client.key)
	CertFileName string // DASHBORG_CERTFILE certificate file, CN must be set to your Dashborg Account Id.  (defaults to dashborg-client.crt)

	// Create a self-signed key/cert if they do not exist.  This will also create a random Account Id.
	// Should only be used with AnonAcc is true.  If AccId is set, will create a key with that AccId
	AutoKeygen bool

	// Set to true to allow other backends in the same zone to call data functions using dash.CallDataHandler
	AllowBackendCalls bool

	// DASHBORG_VERBOSE, set to true for extra debugging information
	Verbose bool

	// close this channel to force a shutdown of the Dashborg Cloud Client
	ShutdownCh chan struct{}

	// These are for internal testing, should not normally be set by clients.
	Env             string // DASHBORG_ENV
	DashborgSrvHost string // DASHBORG_PROCHOST
	DashborgSrvPort int    // DASHBORG_PROCPORT

	setupDone bool // internal

	NoShowJWT   bool          // set to true to disable showing app-link with jwt param
	JWTDuration time.Duration // defaults to 24*time.Hour
	JWTUserId   string        // defaults to "jwt-user"
	JWTRole     string        // defaults to "user"
}

type Container interface {
	OpenApp(appName string) (*dash.App, error)
	WriteApp(acfg dash.AppConfig) error
	RemoveApp(appName string) error

	// Call to connect an app to this container
	ConnectApp(app dash.AppRuntime) error
	ConnectAppRuntime(app dash.AppRuntime) error

	// Dashborg method for returning what apps are currently connected to this container's app/zone.
	ReflectZone() (*ReflectZoneType, error)

	// Dashborg method for forcing all frontend clients to make the specified handler call.
	BackendPush(appName string, path string, data interface{}) error

	// Dashborg method for starting a bare stream that is not connected to a request or frontend.
	StartBareStream(panelName string, streamOpts dash.StreamOpts) (*dash.Request, error)

	// Wait for shutdown
	WaitForShutdown() error

	AppBlobManager(app *dash.App) dash.BlobManager
}

func MakeClient(config *Config) (Container, error) {
	config.setDefaultsAndLoadKeys()
	container := makeCloudClient(config)
	container.startClient()
	return container, nil
}

type ReflectZoneType struct {
	AccId    string                     `json:"accid"`
	ZoneName string                     `json:"zonename"`
	Procs    map[string]ReflectProcType `json:"procs"`
	Apps     map[string]ReflectAppType  `json:"apps"`
}

type ReflectAppType struct {
	AppName    string   `json:"appname"`
	ProcRunIds []string `json:"procrunids"`
}

type ReflectProcType struct {
	StartTs   int64             `json:"startts"`
	ProcName  string            `json:"procname"`
	ProcTags  map[string]string `json:"proctags"`
	ProcRunId string            `json:"procrunid"`
}
