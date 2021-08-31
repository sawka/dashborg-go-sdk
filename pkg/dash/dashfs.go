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
)

const (
	FileTypeStatic  = "static"
	FileTypeRuntime = "runtime"
	FileTypeDir     = "dir"
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

type FileOpts struct {
	Sha256       string      `json:"sha256"`
	Size         int64       `json:"size"`
	FileType     string      `json:"filetype"`
	MimeType     string      `json:"mimetype"`
	AllowedRoles []string    `json:"allowedroles,omitempty"`
	Display      string      `json:"display,omitempty"`
	Metadata     interface{} `json:"metadata,omitempty"`
	Description  string      `json:"description,omitempty"`
	MkDirs       bool        `json:"mkdirs,omitempty"`
	Hidden       bool        `json:"hidden,omitempty"`
}

type AppOpts struct {
	AppTitle    string  `json:"apptitle"`
	AppVisType  string  `json:"appvistype"`
	AppVisOrder float64 `json:"appvisorder"`
	InitPath    string  `json:"initpath"`
	ConnPath    string  `json:"connpath"`
}

type DirOpts struct {
	RoleName   string `json:"rolename"`
	ShowHidden bool   `json:"showhidden"`
	Recursive  bool   `json:"recursive"`
}

type WatchOpts struct {
	ShutdownCh   chan struct{}
	ThrottleTime time.Duration
}

type DashFS interface {
	SetRawPath(path string, r io.Reader, createOpts *FileOpts) error
	SetJsonPath(path string, data interface{}, createOpts *FileOpts) error
	SetPathFromFile(path string, fileName string, createOpts *FileOpts) error
	WatchFile(path string, fileName string, fileOpts *FileOpts, watchOpts *WatchOpts) error

	// LinkPath(path string, fn interface{}, createOpts *FileOpts) error
	// LinkRuntime(path string, runtime AppRuntime, createOpts *FileOpts) error
	// RemovePath(path string) error
	// FileInfo(path string) (*FileInfo, error)
	// DirInfo(path string, dirOpts *DirOpts) (*FileInfo, []*FileInfo, error)

	// TxStart() (DashFS, error)
	// TxCommit() error
	// TxRollback() error
}

type AppFS interface {
	CreateApp(appName string, app *AppOpts) error
	RemoveApp(appName string)
}

// type - static, runtime, dir

type fsImpl struct {
	api InternalApi
}

func (fs *fsImpl) SetRawPath(path string, r io.Reader, fileOpts *FileOpts) error {
	return fs.api.SetRawPath(path, r, fileOpts)
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
	fileOpts.MimeType = "application/json"
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
		watchOpts = &WatchOpts{}
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
