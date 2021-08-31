package dash

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"

	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

type BlobManager interface {
	SetRawBlobData(blobData BlobData, reader io.Reader) error
	SetJsonBlob(extBlobKey string, data interface{}, metadata interface{}) error
	SetBlobDataFromFile(key string, mimeType string, fileName string, metadata interface{}) error
	RemoveBlob(extBlobKey string) error
	ClearExistingBlobs()
	ListBlobs() ([]BlobData, error)
}

type appBlobManager struct {
	api InternalApi
	app *App
}

// Will call Seek(0, 0) on the reader twice, once at the beginning and once at the end.
// If an error is returned, the seek position is not specified.  If no error is returned
// the reader will be reset to the beginning.
// A []byte can be wrapped in a bytes.Buffer to use this function (error will always be nil)
func BlobDataFromReadSeeker(extBlobKey string, mimeType string, r io.ReadSeeker) (BlobData, error) {
	blobNs, blobKey, err := dashutil.ParseExtBlobKey(extBlobKey)
	if err != nil {
		return BlobData{}, dasherr.ValidateErr(fmt.Errorf("Invalid BlobKey"))
	}
	if blobNs == "" {
		blobNs = appBlobNs
	}
	_, err = r.Seek(0, 0)
	if err != nil {
		return BlobData{}, err
	}
	h := sha256.New()
	numCopyBytes, err := io.Copy(h, r)
	if err != nil {
		return BlobData{}, err
	}
	hashVal := h.Sum(nil)
	hashValStr := base64.StdEncoding.EncodeToString(hashVal[:])
	_, err = r.Seek(0, 0)
	if err != nil {
		return BlobData{}, err
	}
	blobData := BlobData{
		BlobNs:   blobNs,
		BlobKey:  blobKey,
		MimeType: mimeType,
		Sha256:   hashValStr,
		Size:     numCopyBytes,
	}
	return blobData, nil
}

// If you only have an io.Reader, this function will call ioutil.ReadAll, read the full stream
// into a []byte, compute the size and SHA-256, and then wrap the []byte in a *bytes.Reader
// suitable to pass to SetRawBlobData()
func BlobDataFromReader(extBlobKey string, mimeType string, r io.Reader) (BlobData, *bytes.Reader, error) {
	barr, err := ioutil.ReadAll(r)
	if err != nil {
		return BlobData{}, nil, err
	}
	breader := bytes.NewReader(barr)
	blobData, err := BlobDataFromReadSeeker(extBlobKey, mimeType, breader)
	if err != nil {
		return BlobData{}, nil, err
	}
	return blobData, breader, nil
}

func makeAppBlobManager(app *App, api InternalApi) *appBlobManager {
	return &appBlobManager{
		app: app,
		api: api,
	}
}

func (abm *appBlobManager) RemoveBlob(extBlobKey string) error {
	blobNs, blobKey, err := dashutil.ParseExtBlobKey(extBlobKey)
	if err != nil {
		return dasherr.ValidateErr(fmt.Errorf("Invalid BlobKey"))
	}
	if blobNs == "" {
		blobNs = appBlobNs
	}
	blobData := BlobData{
		BlobNs:  blobNs,
		BlobKey: blobKey,
	}
	return abm.api.RemoveBlob(abm.app.appConfig, blobData)
}

// SetRawBlobData blobData must have BlobKey, MimeType, Size, and Sha256 set.
// Clients will normally call SetBlobDataFromFile, or construct a BlobData
// from calling BlobDataFromReadSeeker or BlobDataFromReader rather than
// creating a BlobData directly.
func (abm *appBlobManager) SetRawBlobData(blobData BlobData, reader io.Reader) error {
	err := abm.api.SetBlobData(abm.app.appConfig, blobData, reader)
	if err != nil {
		log.Printf("Dashborg error setting blob data app:%s blobkey:%s err:%v\n", abm.app.appConfig.AppName, blobData.ExtBlobKey(), err)
		return err
	}
	return nil
}

func (abm *appBlobManager) SetJsonBlob(extBlobKey string, data interface{}, metadata interface{}) error {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(data)
	if err != nil {
		return dasherr.JsonMarshalErr("BlobData", err)
	}
	reader := bytes.NewReader(jsonBuf.Bytes())
	blob, err := BlobDataFromReadSeeker(extBlobKey, jsonMimeType, reader)
	if err != nil {
		return err
	}
	blob.Metadata = metadata
	return abm.SetRawBlobData(blob, reader)
}

func (abm *appBlobManager) ClearExistingBlobs() {
	abm.app.appConfig.ClearExistingBlobs = true
}

func (abm *appBlobManager) SetBlobDataFromFile(key string, mimeType string, fileName string, metadata interface{}) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	blobData, err := BlobDataFromReadSeeker(key, mimeType, fd)
	if err != nil {
		return err
	}
	blobData.Metadata = metadata
	return abm.SetRawBlobData(blobData, fd)
}

func (abm *appBlobManager) ListBlobs() ([]BlobData, error) {
	return abm.api.ListBlobs(abm.app.appConfig.AppName, abm.app.appConfig.AppVersion)
}
