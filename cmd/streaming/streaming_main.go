package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashcloud"
	"github.com/sawka/dashborg-go-sdk/pkg/dashutil"
)

const MAX_RUNLENGTH = 24 * 60 * 60

var ValidJobTypes map[string]bool = map[string]bool{
	"randomwalk":    true,
	"randomwalk-x":  true,
	"randomwalk-y":  true,
	"randomwalk-xy": true,
}

type StreamModel struct {
	Lock        *sync.Mutex
	RunningJobs map[string]*Job
}

type Job struct {
	Lock      *sync.Mutex        `json:"-"`
	JobId     string             // uuid
	StreamReq *dash.PanelRequest `json:"-"`
	StartTs   int64
	DoneTs    int64
	LogFile   string
	JobType   string
	JobStatus string
	RunLength int
	XBias     bool
	YBias     bool

	CurIter      int
	Distribution []int
	CurX         int
	CurY         int
}

func boundInt(v int, min int, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (j *Job) CopyJob() *Job {
	j.Lock.Lock()
	defer j.Lock.Unlock()

	rtn := &Job{}
	*rtn = *j
	rtn.Lock = &sync.Mutex{}
	rtn.Distribution = make([]int, 11*11)
	rtn.StreamReq = nil
	copy(rtn.Distribution, j.Distribution)
	return rtn
}

func (j *Job) Run(c dash.Container) {
	defer j.StreamReq.Done()
	for j.CurIter < j.RunLength {
		time.Sleep(1 * time.Second)
		ok := j.RunIteration()
		j.StreamReq.SetData("$.seljob", j.CopyJob())
		j.StreamReq.Flush()
		if !ok {
			break
		}
	}

	j.Lock.Lock()
	if j.JobStatus == "running" {
		j.JobStatus = "done"
	}
	j.DoneTs = dashutil.Ts()
	j.Lock.Unlock()
	j.StreamReq.SetData("$.seljob", j.CopyJob())
	j.StreamReq.Flush()
	c.BackendPush("streaming", "/refresh-job-list", nil)
}

func randomIncrement(bias bool) int {
	rtn := rand.Intn(3) - 1
	if bias && rtn < 1 {
		if rand.Intn(10) == 0 {
			rtn++
		}
	}
	return rtn
}

func (j *Job) RunIteration() bool {
	j.Lock.Lock()
	defer j.Lock.Unlock()
	if rand.Intn(2) == 0 {
		j.CurX = boundInt(j.CurX+randomIncrement(j.XBias), -5, 5)
	} else {
		j.CurY = boundInt(j.CurY+randomIncrement(j.YBias), -5, 5)
	}
	j.CurIter++
	j.UpdateDist()
	return j.JobStatus == "running"
}

// must already hold lock
func (j *Job) UpdateDist() {
	j.Distribution[(j.CurY+5)*11+j.CurX+5]++
}

func CreateStreamModel() (*StreamModel, error) {
	rtn := &StreamModel{}
	rtn.Lock = &sync.Mutex{}
	rtn.RunningJobs = make(map[string]*Job)
	return rtn, nil
}

type StartJobParams struct {
	JobType   string `json:"jobtype"`
	RunLength int    `json:"runlength,string"`
}

type PanelState struct {
	Streaming   bool   `json:"streaming"`
	SelJobId    string `json:"seljobid"`
	OnlyRunning bool   `json:"onlyrunning"`
}

func (m *StreamModel) NumRunningJobs() int {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	numRunning := 0
	for _, j := range m.RunningJobs {
		if j.JobStatus == "running" {
			numRunning++
		}
	}
	return numRunning
}

func (m *StreamModel) NumJobs() int {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	return len(m.RunningJobs)
}

func (m *StreamModel) StartJob(req *dash.PanelRequest, state PanelState, data StartJobParams) error {
	if data.JobType == "" || !ValidJobTypes[data.JobType] {
		return errors.New("Invalid JobType")
	}
	if data.RunLength <= 0 || data.RunLength > MAX_RUNLENGTH {
		return errors.New("Invalid RunLength")
	}
	if m.NumRunningJobs() >= 10 {
		return errors.New("Maximum of 10 running jobs (stop a job to start a new one)")
	}
	if m.NumJobs() >= 50 {
		return errors.New("Maximum of 50 jobs (delete a job to start a new one)")
	}
	jobId := uuid.New().String()
	job := &Job{
		Lock:         &sync.Mutex{},
		JobId:        jobId,
		StartTs:      dashutil.Ts(),
		JobType:      data.JobType,
		JobStatus:    "running",
		RunLength:    data.RunLength,
		Distribution: make([]int, 11*11),
	}
	if job.JobType == "randomwalk-x" || job.JobType == "randomwalk-xy" {
		job.XBias = true
	}
	if job.JobType == "randomwalk-y" || job.JobType == "randomwalk-xy" {
		job.YBias = true
	}
	job.UpdateDist()
	var err error
	job.StreamReq, err = req.Container().StartBareStream("streaming", dash.StreamOpts{StreamId: job.JobId, NoServerCancel: true})
	if err != nil {
		return err
	}

	m.Lock.Lock()
	m.RunningJobs[job.JobId] = job
	defer m.Lock.Unlock()

	req.InvalidateData("/get-jobs")
	req.SetData("$state.seljobid", jobId)
	req.SetData("$.seljob", job)

	req.SetData("$state.jobstreams", nil)
	if state.Streaming {
		cpath := fmt.Sprintf("$state.jobstreams['" + job.JobId + "']")
		req.StartStream(dash.StreamOpts{StreamId: job.JobId, ControlPath: cpath}, nil)
	}

	go job.Run(req.Container())
	return nil
}

type runningJobRtn struct {
	JobId      string
	JobType    string
	ShortJobId string
	StartTs    int64
	JobStatus  string
	RunLength  int
	CurIter    int
}

func (m *StreamModel) GetJobList(req *dash.PanelRequest, state PanelState) (interface{}, error) {
	m.Lock.Lock()
	defer m.Lock.Unlock()

	rtn := make([]runningJobRtn, 0)
	for _, j := range m.RunningJobs {
		if state.OnlyRunning && j.JobStatus != "running" {
			continue
		}
		jrtn := runningJobRtn{
			JobId:      j.JobId,
			JobType:    j.JobType,
			ShortJobId: j.JobId[0:8],
			StartTs:    j.StartTs,
			JobStatus:  j.JobStatus,
			RunLength:  j.RunLength,
			CurIter:    j.CurIter,
		}
		rtn = append(rtn, jrtn)
	}
	sort.Slice(rtn, func(i int, j int) bool {
		return rtn[j].StartTs < rtn[i].StartTs
	})
	return rtn, nil
}

func (m *StreamModel) GetJobById(req *dash.PanelRequest, state interface{}, jobId string) (interface{}, error) {
	if jobId == "" {
		return nil, nil
	}
	m.Lock.Lock()
	j := m.RunningJobs[jobId]
	m.Lock.Unlock()
	if j == nil {
		return nil, nil
	}
	return j.CopyJob(), nil
}

func (m *StreamModel) SelectJob(req *dash.PanelRequest, state PanelState, jobId string) error {
	req.SetData("$.seljob", nil)
	if jobId == "" {
		return nil
	}
	m.Lock.Lock()
	j := m.RunningJobs[jobId]
	m.Lock.Unlock()
	if j == nil {
		return nil
	}
	j = j.CopyJob()
	req.SetData("$.seljob", j)
	req.SetData("$state.jobstreams", nil)
	if state.Streaming && j.JobStatus == "running" {
		cpath := fmt.Sprintf("$state.jobstreams['" + j.JobId + "']")
		req.StartStream(dash.StreamOpts{StreamId: j.JobId, ControlPath: cpath}, nil)
	}
	return nil
}

func (m *StreamModel) StopJob(req *dash.PanelRequest, state PanelState, jobId string) error {
	if jobId == "" {
		return nil
	}
	m.Lock.Lock()
	j := m.RunningJobs[jobId]
	m.Lock.Unlock()
	if j == nil {
		return nil
	}
	j.Lock.Lock()
	j.JobStatus = "stopped"
	j.Lock.Unlock()
	time.Sleep(2 * time.Millisecond)
	req.InvalidateData("/get-jobs")
	req.SetData("$state.seljob", j.CopyJob())
	return nil
}

func (m *StreamModel) DeleteJob(req *dash.PanelRequest, state PanelState, jobId string) error {
	if jobId == "" {
		return nil
	}
	m.Lock.Lock()
	defer m.Lock.Unlock()

	j := m.RunningJobs[jobId]
	if j == nil {
		return nil
	}
	j.Lock.Lock()
	defer j.Lock.Unlock()
	if j.JobStatus == "running" {
		return nil
	}
	delete(m.RunningJobs, jobId)
	req.SetData("$state.seljobid", nil)
	req.SetData("$.seljob", nil)
	req.InvalidateData("/get-jobs")
	return nil
}

func (m *StreamModel) ToggleStreaming(req *dash.PanelRequest, state PanelState) error {
	req.SetData("$state.jobstreams", nil)
	if state.Streaming {
		cpath := fmt.Sprintf("$state.jobstreams['" + state.SelJobId + "']")
		req.StartStream(dash.StreamOpts{StreamId: state.SelJobId, ControlPath: cpath}, nil)
	}
	return nil
}

func (m *StreamModel) RefreshJobList(req *dash.PanelRequest) error {
	req.InvalidateData("/get-jobs")
	return nil
}

func (m *StreamModel) Init(req *dash.PanelRequest) error {
	req.SetData("$state.streaming", true)
	var job *Job
	m.Lock.Lock()
	for _, j := range m.RunningJobs {
		if job == nil {
			job = j
			continue
		}
		if j.StartTs > job.StartTs {
			job = j
		}
	}
	m.Lock.Unlock()
	if job == nil {
		return nil
	}
	job = job.CopyJob()
	req.SetData("$state.seljobid", job.JobId)
	req.SetData("$.seljob", job)
	if job.JobStatus == "running" {
		cpath := fmt.Sprintf("$state.jobstreams['" + job.JobId + "']")
		err := req.StartStream(dash.StreamOpts{StreamId: job.JobId, ControlPath: cpath}, nil)
		if err != nil {
			fmt.Printf("hello error %v\n", err)
		}
	}
	return nil
}

func main() {
	model, err := CreateStreamModel()
	if err != nil {
		fmt.Printf("Error creating stream model: %v\n", err)
		return
	}
	rand.Seed(time.Now().Unix())

	app := dash.MakeApp("streaming")
	app.SetHtmlFromFile("cmd/streaming/streaming.html")
	app.HandlerEx("/init", model.Init)
	app.DataHandlerEx("/get-job", model.GetJobById)
	app.DataHandlerEx("/get-jobs", model.GetJobList)
	app.HandlerEx("/start-job", model.StartJob)
	app.HandlerEx("/select-job", model.SelectJob)
	app.HandlerEx("/toggle-streaming", model.ToggleStreaming)
	app.HandlerEx("/stop-job", model.StopJob)
	app.HandlerEx("/delete-job", model.DeleteJob)
	app.HandlerEx("/refresh-job-list", model.RefreshJobList)
	app.SetOnLoadHandler("/init")

	cfg := &dash.Config{ProcName: "streaming", AnonAcc: true, AutoKeygen: true}
	container, _ := dashcloud.MakeClient(cfg)
	container.ConnectApp(app)

	time.Sleep(5 * time.Second)
	container.BackendPush("streaming", "/refresh-job-list", nil)
	select {}
}
