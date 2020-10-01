package main

import (
	"fmt"
	"os"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func RunProcess1() {
	panel, _ := dash.LookupPanel("demo1")
	logger := panel.LookupControl("log", "demo-log2")
	logger.LogText("Running Process #1")
	// run the actual process
	p := logger.LogControl("<progress>[@progresslabel=P1 @progressmax=10]")
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		p.ProgressSet(i+1, "running")
	}
	p.ProgressDone()
	// p.UpdateProgress(transport.ProgressData{Done: true, ClearStatus: true})
	logger.LogText("Process #1 Done")
}

func Control() {
	panel, _ := dash.LookupPanel("demo1")
	logger := panel.LookupControl("log", "demo-log2")
	logger.LogText("demo1 restarted")
	stopButton := panel.LookupControl("button", "b-stop")
	b1 := panel.LookupControl("button", "b-1")
	ch := make(chan bool)
	stopButton.OnClick(func() error {
		logger.LogText("Stop Button Clicked\n")
		close(ch)
		return nil
	})
	b1.OnClick(func() error {
		RunProcess1()
		return nil
	})
	<-ch
}

func DefinePanelHW() {
	panel := dash.DefinePanel("demo1")
	panel.Print("hello world")
	panel.Flush()
}

func DefinePanel() {
	panel := dash.DefinePanel("demo1")
	panel.Print("<div>[rootdiv @col @padding=10px @height=100%]")
	panel.Print("[@h1] Demo Dashboard")
	panel.Print("<div>[@margintop=10px @marginbottom=10px]")
	panel.Print("  <button b-1/>[@primary] Run Process #1")
	panel.Print("  <button b-stop/>[@marginleft=15px] Stop")
	panel.Print("</div>")
	panel.Print("<log demo-log2/>[@logstyle @grow]")
	// panel.Print("<progress/>")
	panel.Print("</div>")
	panel.Flush()
	panel.Dump(os.Stdout)
	fmt.Printf("Panel Link %s\n", panel.PanelLink())
}

func main() {
	cfg := &dash.Config{ProcName: "demo1", AnonAcc: true, Env: "dev"}
	cfg.UseAnonKeys()
	defer dash.StartProcClient(cfg).WaitForClear()

	DefinePanel()
	Control()
}
