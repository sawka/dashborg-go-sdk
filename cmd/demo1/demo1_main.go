package main

import (
	"fmt"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

const PANEL_HTML = `
<panel>
  <h1>Demo Dashboard</h1>
  <d-button handler="/run-action">Run Action</d-button>
</panel>
`

func main() {
	cfg := &dash.Config{ProcName: "demo1", AnonAcc: true, AutoKeygen: true}
	dash.StartProcClient(cfg)
	defer dash.WaitForClear()
	dash.RegisterPanelHandler("demo1", "/", func(req *dash.PanelRequest) error {
		req.NoAuth()
		req.SetHtml(PANEL_HTML)
		return nil
	})
	dash.RegisterPanelHandler("demo1", "/run-action", func(req *dash.PanelRequest) error {
		fmt.Printf("running backend action\n")
		return nil
	})
	select {}
}
