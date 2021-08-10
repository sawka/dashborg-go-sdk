package dash

import "io"

const ClientVersion = "go-0.6.0"

const (
	RoleSuper  = "*"
	RolePublic = "public"
	RoleUser   = "user"
)

const (
	AccTypeAnon = "anon"
	AccTypeFree = "free"
)

type Container interface {
	ConnectApp(app AppRuntime) error
	StartBareStream(appName string, streamOpts StreamOpts) (*Request, error)
	BackendPush(appName string, path string, data interface{}) error
	WaitForShutdown() error
	SetBlobData(acfg AppConfig, blob BlobData, r io.Reader) error
}

type StreamOpts struct {
	StreamId       string `json:"streamid"`       // if unset will be set to a random uuid
	ControlPath    string `json:"controlpath"`    // control path for client cancelation
	NoServerCancel bool   `json:"noservercancel"` // set to true to keep running the stream, even when there are no clients listening (or on server error)
}
