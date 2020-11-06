package main

import "github.com/sawka/dashborg-go-sdk/pkg/dash"

func main() {
	cfg := &dash.Config{ProcName: "hello", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()
	pw := dash.DefinePanel("default")
	pw.Print("[@h1] Hello World")
	pw.Print("<div>[row]")
	pw.Print("<button b1/> Click Me!")
	pw.Print("<button b2/> Button 2")
	pw.Print("<button b3/> B3")
	pw.Print("</div>")
	pw.Print("<log test-logger/>[@grow]")
	p, _ := pw.Flush()
	btn := p.LookupControl("button", "b1")
	logger := p.LookupControl("log", "test-logger")
	btn.OnClick(func() error {
		logger.LogText("button clicked")
		return nil
	})
	select {} // block forever
}
