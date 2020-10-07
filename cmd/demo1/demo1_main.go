package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func RunProcess1() {
	panel, _ := dash.LookupPanel("demo1")
	logger := panel.LookupControl("log", "demo-log2")
	logger.LogText("Running Process #1")
	// run the actual process
	p := logger.LogControl("<progress/>[@progresslabel=P1 @progressmax=10]")
	if p == nil {
		fmt.Printf("process1 - nil return from LogControl\n")
		return
	}
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		p.ProgressSet(i+1, "running")
	}
	p.ProgressDone()
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
	panel.Print("[@h1] Demo Dashboard")
	panel.Print("<div>[row]")
	panel.Print("  <button b-1/> Run Process #1")
	panel.Print("  <button b-stop/> Stop")
	panel.Print("</div>")
	panel.Print("<log demo-log2/>[logstyle @grow]")
	panel.Flush()
	panel.Dump(os.Stdout)
	log.Printf("Panel Link %s\n", panel.PanelLink())
}

func main() {
	cfg := &dash.Config{ProcName: "demo1", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()

	DefinePanel()
	Control()
}
