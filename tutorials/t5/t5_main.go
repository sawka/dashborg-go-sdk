package main

import "github.com/sawka/dashborg-go-sdk/pkg/dash"

func main() {
	cfg := &dash.Config{ProcName: "hello", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()
	pw := dash.DefinePanel("default")
	pw.Print("[@h1] Inputs Tutorial")
	pw.Print("<handler /test />")
	pw.Print("<div>[row]")
	pw.Print("<input:text/>[@form=f1 @formfield=name @placeholder=Name]")
	pw.Print("<inputselect>[@form=f1 @formfield=color @placeholder='Pick a Color']")
	pw.Print("  <option/>[@value=b] Blue")
	pw.Print("  <option/>[@value=g] Green")
	pw.Print("  <option/>[@value=r] Red")
	pw.Print("</inputselect>")
	pw.Print("<button b1/>[@handler=/test/submit @dataform=f1] Submit")
	pw.Print("</div>")
	pw.Print("<log test-logger/>[@grow]")
	p, _ := pw.Flush()
	logger := p.LookupControl("log", "test-logger")
	p.OnRequest("/test/submit", func(req *dash.PanelRequest) error {
		logger.LogText("got request %s, with data %v\n", req.HandlerPath, req.Data)
		return nil
	})
	select {} // block forever
}
