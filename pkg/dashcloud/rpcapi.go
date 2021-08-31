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

func (rpc *InternalApi) StartStreamProtoRpc(m *dashproto.StartStreamMessage) (string, error) {
	return rpc.client.startStreamProtoRpc(m)
}

func (rpc *InternalApi) RemoveBlob(acfg dash.AppConfig, blob dash.BlobData) error {
	return rpc.client.removeBlob(acfg, blob)
}

func (rpc *InternalApi) SetBlobData(acfg dash.AppConfig, blobData dash.BlobData, r io.Reader) error {
	return rpc.client.setBlobData(acfg, blobData, r)
}

func (rpc *InternalApi) StartBareStream(appName string, streamOpts dash.StreamOpts) (*dash.AppRequest, error) {
	return rpc.client.startBareStream(appName, streamOpts)
}

func (rpc *InternalApi) CallDataHandler(appName string, path string, data interface{}) (interface{}, error) {
	return rpc.client.callDataHandler(appName, path, data)
}

func (rpc *InternalApi) BackendPush(appName string, path string, data interface{}) error {
	return rpc.client.backendPush(appName, path, data)
}

func (rpc *InternalApi) ListBlobs(appName string, appVersion string) ([]dash.BlobData, error) {
	return rpc.client.listBlobs(appName, appVersion)
}

func (rpc *InternalApi) SetRawPath(path string, r io.Reader, fileOpts *dash.FileOpts) error {
	return rpc.client.setRawPath(path, r, fileOpts)
}

func (rpc *InternalApi) RemovePath(path string) error {
	return rpc.client.removePath(path)
}

func (rpc *InternalApi) FileInfo(path string, dirOpts *dash.DirOpts) ([]*dash.FileInfo, error) {
	return rpc.client.fileInfo(path, dirOpts)
}
