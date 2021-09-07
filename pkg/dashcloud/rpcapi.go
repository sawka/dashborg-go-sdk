package dashcloud

import (
	"io"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
)

// Used for low-level communication between AppClient and the Dashborg Cloud Service.
// Not for use by general SDK users.  API not stable and subject to change
type InternalApi struct {
	client *DashCloudClient
}

// SendResponseProtoRpc is for internal use by the Dashborg AppClient, not to be called by the end user.
func (rpc *InternalApi) SendResponseProtoRpc(m *dashproto.SendResponseMessage) (int, error) {
	return rpc.client.sendResponseProtoRpc(m)
}

func (rpc *InternalApi) BackendPush(appName string, path string, data interface{}) error {
	return rpc.client.backendPush(appName, path, data)
}

func (rpc *InternalApi) SetRawPath(path string, r io.Reader, fileOpts *dash.FileOpts, linkRt dash.LinkRuntime) error {
	return rpc.client.SetRawPath(path, r, fileOpts, linkRt)
}

func (rpc *InternalApi) RemovePath(path string) error {
	return rpc.client.removePath(path)
}

func (rpc *InternalApi) FileInfo(path string, dirOpts *dash.DirOpts) ([]*dash.FileInfo, error) {
	return rpc.client.fileInfo(path, dirOpts)
}

func (rpc *InternalApi) MakeUrl(appNameOrPath string, showJwt bool) (string, error) {
	return rpc.client.MakeUrl(appNameOrPath, showJwt)
}
