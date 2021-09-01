package dash

const ClientVersion = "go-0.6.2"

const (
	RoleSuper  = "*"
	RolePublic = "public"
	RoleUser   = "user"
)

const (
	AccTypeAnon       = "anon"
	AccTypeFree       = "free"
	AccTypePro        = "pro"
	AccTypeEnterprise = "enterprise"
)

const RtnSetDataPath = "@rtn"

type StreamOpts struct {
	StreamId       string `json:"streamid"`       // if unset will be set to a random uuid
	ControlPath    string `json:"controlpath"`    // control path for client cancelation
	NoServerCancel bool   `json:"noservercancel"` // set to true to keep running the stream, even when there are no clients listening (or on server error)
}
