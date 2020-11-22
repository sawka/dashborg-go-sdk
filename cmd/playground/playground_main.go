package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func DmlPlayground() {
	panel := dash.DefinePanel("playground")
	panel.Print("[@h1] Dashborg Playground")
	panel.Print("<div>[row @grow]")
	panel.Print("  <handler /playground/>")
	panel.Print("  <div>[col @width=700px @borderright='1px solid black' @paddingright=10px]")
	panel.Print("    <input:textarea dmlinput/>[@block @form=f1 @formfield=text @width=100% @height=50% @fixedfont @defaultvalue='[@h1] Playground Test']")
	panel.Print("    <button/>[@handler=/playground/submit @dataform=f1] Submit")
	panel.Print("    <context errors/>[col]")
	panel.Print("  </div>")
	panel.Print("  <div>[col @grow]")
	panel.Print("    <context output>[col]")
	panel.Print("      [@h1] Playground Test")
	panel.Print("    </context>")
	panel.Print("  </div>")
	panel.Print("</div>")
	p, _ := panel.Flush()
	p.OnRequest("/playground/submit", func(req *dash.PanelRequest) error {
		var inputDml string
		req.DecodeDataPath("text", &inputDml)
		ctx := req.LookupContext("output")
		ctx.PrintMulti(inputDml)
		ctx.Flush()
		ectx := req.LookupContext("errors")
		if ctx.HasErrors() {
			var errBuf bytes.Buffer
			ctx.ReportErrors(&errBuf)
			errStr := errBuf.String()
			errParts := strings.Split(errStr, "\n")
			ectx.Print("[@h3] Parse Errors")
			for _, line := range errParts {
				ectx.Print(fmt.Sprintf("[@fixedfont] %s", line))
			}
			ectx.Flush()
		} else {
			ectx.Revert()
		}
		return nil
	})
}

func main() {
	cfg := &dash.Config{ProcName: "playground", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()
	DmlPlayground()
	select {}
}
