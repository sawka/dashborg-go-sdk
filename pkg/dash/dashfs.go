package dash

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sawka/dashborg-go-sdk/pkg/dasherr"
	"github.com/sawka/dashborg-go-sdk/pkg/dashproto"
)

const (
	MimeTypeDashborgHtml = "text/x-dashborg-html"
	MimeTypeHtml         = "text/html"
	MimeTypeJson         = "application/json"
	MimeTypeDashborgApp  = "application/x-dashborg+json"
)

const (
	FileTypeStatic         = "static"
	FileTypeRuntimeLink    = "rt-link"
	FileTypeAppRuntimeLink = "rt-app-link"
	FileTypeDir            = "dir"
)

type FileInfo struct {
	Path         string      `json:"path"`
	Size         int64       `json:"size"`
	CreatedTs    int64       `json:"createdts"`
	UpdatedTs    int64       `json:"updatedts"`
	Sha256       string      `json:"sha256"`
	FileType     string      `json:"filetype"`
	MimeType     string      `json:"mimetype"`
	AllowedRoles []string    `json:"allowedroles"`
	Display      string      `json:"display,omitempty"`
	Metadata     interface{} `json:'metadata,omitempty"`
	Description  string      `json:"description,omitempty"`
	Hidden       bool        `json:"hidden,omitempty"`
	Removed      bool        `json:"removed,omitempty"`
	ProcLinks    []string    `json:"proclinks,omitempty"`
	TxId         string      `json:"txid,omitempty"`
}

type BlobReturn struct {
	Reader   io.Reader
	MimeType string
}

type FileOpts struct {
	FileType     string      `json:"filetype"`
	Sha256       string      `json:"sha256"`
	Size         int64       `json:"size"`
	MimeType     string      `json:"mimetype"`
	AllowedRoles []string    `json:"allowedroles,omitempty"`
	Display      string      `json:"display,omitempty"`
	Metadata     interface{} `json:"metadata,omitempty"`
	Description  string      `json:"description,omitempty"`
	MkDirs       bool        `json:"mkdirs,omitempty"`
	Hidden       bool        `json:"hidden,omitempty"`
}

func (opts *FileOpts) IsLinkType() bool {
	return opts.FileType == FileTypeRuntimeLink || opts.FileType == FileTypeAppRuntimeLink
}

type DirOpts struct {
	RoleList   []string `json:"rolelist"`
	ShowHidden bool     `json:"showhidden"`
	Recursive  bool     `json:"recursive"`
}

type WatchOpts struct {
	ThrottleTime time.Duration
	ShutdownCh   chan struct{}
}

type DashFS interface {
	SetRawPath(path string, r io.Reader, fileOpts *FileOpts, runtime LinkRuntime) error
	SetStaticPath(path string, r io.ReadSeeker, fileOpts *FileOpts) error
	SetJsonPath(path string, data interface{}, fileOpts *FileOpts) error
	SetPathFromFile(path string, fileName string, fileOpts *FileOpts) error
	WatchFile(path string, fileName string, fileOpts *FileOpts, watchOpts *WatchOpts) error
	LinkRuntime(path string, runtime LinkRuntime, fileOpts *FileOpts) error
	LinkAppRuntime(path string, apprt LinkRuntime, fileOpts *FileOpts) error
	RemovePath(path string) error
	FileInfo(path string) (*FileInfo, error)
	DirInfo(path string, dirOpts *DirOpts) ([]*FileInfo, error)
}

// internal callbacks into DashCloud package.  Not for use by end-user, API subject to change.
type InternalApi interface {
	SendResponseProtoRpc(m *dashproto.SendResponseMessage) (int, error)
	SetRawPath(path string, r io.Reader, fileOpts *FileOpts, rt LinkRuntime) error
	RemovePath(path string) error
	FileInfo(path string, dirOpts *DirOpts) ([]*FileInfo, error)
}

// type - static, runtime, dir

type fsImpl struct {
	api InternalApi
}

func (fs *fsImpl) SetRawPath(path string, r io.Reader, fileOpts *FileOpts, runtime LinkRuntime) error {
	return fs.api.SetRawPath(path, r, fileOpts, runtime)
}

func (fs *fsImpl) SetJsonPath(path string, data interface{}, fileOpts *FileOpts) error {
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(data)
	if err != nil {
		return dasherr.JsonMarshalErr("JsonData", err)
	}
	reader := bytes.NewReader(jsonBuf.Bytes())
	if fileOpts == nil {
		fileOpts = &FileOpts{}
	}
	err = UpdateFileOptsFromReadSeeker(reader, fileOpts)
	if err != nil {
		return err
	}
	if fileOpts.MimeType == "" {
		fileOpts.MimeType = MimeTypeJson
	}
	return fs.SetRawPath(path, reader, fileOpts)
}

func (fs *fsImpl) SetPathFromFile(path string, fileName string, fileOpts *FileOpts) error {
	fd, err := os.Open(fileName)
	if err != nil {
		return err
	}
	err = UpdateFileOptsFromReadSeeker(fd, fileOpts)
	if err != nil {
		return err
	}
	return fs.SetRawPath(path, fd, fileOpts)
}

// Will call Seek(0, 0) on the reader twice, once at the beginning and once at the end.
// If an error is returned, the seek position is not specified.  If no error is returned
// the reader will be reset to the beginning.
// A []byte can be wrapped in a bytes.Buffer to use this function (error will always be nil)
func UpdateFileOptsFromReadSeeker(r io.ReadSeeker, fileOpts *FileOpts) error {
	_, err := r.Seek(0, 0)
	if err != nil {
		return err
	}
	h := sha256.New()
	numCopyBytes, err := io.Copy(h, r)
	if err != nil {
		return err
	}
	hashVal := h.Sum(nil)
	hashValStr := base64.StdEncoding.EncodeToString(hashVal[:])
	_, err = r.Seek(0, 0)
	if err != nil {
		return err
	}
	fileOpts.FileType = FileTypeStatic
	fileOpts.Sha256 = hashValStr
	fileOpts.Size = numCopyBytes
	return nil
}

func MakeDashFS(api InternalApi) DashFS {
	return &fsImpl{api: api}
}

func (fs *fsImpl) runWatchedSetPath(path string, fileName string, fileOpts *FileOpts) {
	err := fs.SetPathFromFile(path, fileName, fileOpts)
	if err != nil {
		log.Printf("Error calling SetPathFromFile (watched file) path=%s file=%s err=%v\n", path, fileName, err)
	} else {
		log.Printf("Watcher called SetPathFromFile path=%s file=%s size=%d hash=%s\n", path, fileName, fileOpts.Size, fileOpts.Sha256)
	}
}

func (fs *fsImpl) WatchFile(path string, fileName string, fileOpts *FileOpts, watchOpts *WatchOpts) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if fileOpts == nil {
		fileOpts = &FileOpts{}
	}
	if watchOpts == nil {
		watchOpts = &WatchOpts{ThrottleTime: time.Second}
	}
	err = fs.SetPathFromFile(path, fileName, fileOpts)
	if err != nil {
		return err
	}
	err = watcher.Add(fileName)
	if err != nil {
		return err
	}
	go func() {
		var needsRun bool
		lastRun := time.Now()
		defer watcher.Close()
		var timer *time.Timer
		for {
			var timerCh <-chan time.Time
			if timer != nil {
				timerCh = timer.C
			}
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op == fsnotify.Write || event.Op == fsnotify.Create {
					dur := time.Since(lastRun)
					if dur < watchOpts.ThrottleTime {
						needsRun = true
						if timer == nil {
							timer = time.NewTimer(watchOpts.ThrottleTime - dur)
						}
					} else {
						needsRun = false
						fs.runWatchedSetPath(path, fileName, fileOpts)
						lastRun = time.Now()
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("DashFS Watch Error path=%s file=%s err=%v\n", path, fileName, err)
				return

			case <-timerCh:
				if needsRun {
					timer = nil
					needsRun = false
					fs.runWatchedSetPath(path, fileName, fileOpts)
					lastRun = time.Now()
				}

			case <-watchOpts.ShutdownCh:
				return
			}
		}
	}()
	return nil
}

func (fs *fsImpl) RemovePath(path string) error {
	return fs.api.RemovePath(path)
}

func (fs *fsImpl) FileInfo(path string) (*FileInfo, error) {
	rtn, err := fs.api.FileInfo(path, nil)
	if err != nil {
		return nil, err
	}
	if len(rtn) == 0 {
		return nil, nil
	}
	return rtn[0], nil
}

func (fs *fsImpl) DirInfo(path string, dirOpts *DirOpts) ([]*FileInfo, error) {
	if dirOpts == nil {
		dirOpts = &DirOpts{}
	}
	return fs.api.FileInfo(path, dirOpts)
}

func (fs *fsImpl) LinkRuntime(path string, rt LinkRuntime, fileOpts *FileOpts) error {
	if hasErr, ok := rt.(HasErr); ok {
		err := hasErr.Err()
		if err != nil {
			return err
		}
	}
	if fileOpts == nil {
		fileOpts = &FileOpts{}
	}
	fileOpts.FileType = FileTypeRuntimeLink
	return fs.api.SetRawPath(path, nil, fileOpts, rt)
}

func (fs *fsImpl) LinkAppRuntime(path string, apprt LinkRuntime, fileOpts *FileOpts) error {
	if hasErr, ok := apprt.(HasErr); ok {
		err := hasErr.Err()
		if err != nil {
			return err
		}
	}
	if fileOpts == nil {
		fileOpts = &FileOpts{}
	}
	fileOpts.FileType = FileTypeAppRuntimeLink
	return fs.api.SetRawPath(path, nil, fileOpts, apprt)
}

func (fs *fsImpl) SetStaticPath(path string, r io.ReadSeeker, fileOpts *FileOpts) error {
	if fileOpts == nil {
		fileOpts = &FileOpts{}
	}
	fileOpts.FileType = FileTypeStatic
	err := UpdateFileOptsFromReadSeeker(r, fileOpts)
	if err != nil {
		return err
	}
	return fs.SetRawPath(path, r, fileOpts)
}
