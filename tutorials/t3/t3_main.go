package main

import "github.com/sawka/dashborg-go-sdk/pkg/dash"

func main() {
	cfg := &dash.Config{ProcName: "hello", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()
	pw := dash.DefinePanel("default")
	pw.Print("[@h1] Hello World")
	btn := pw.Print("<button/> Click Me!")
	logger := pw.Print("<log/>[@grow]")
	pw.Flush()
	btn.OnClick(func() error {
		logger.LogText("button clicked")
		return nil
	})
	select {} // block forever
}
