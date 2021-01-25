package main

import (
	"encoding/json"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

const INITIAL_HTML = `
<h1>Test Output</h1>
<box class="row" alignitems="center">
  <d-button>Test Button</d-button>
  <d-box color="*$.data.color">
    <d-text bind="$.data.text"/>
  </d-box>
</box>
`

const INITIAL_DATA = `
{
  "text": "Dyn Text",
  "color": "red"
}
`

type PlaygroundModel struct {
	Html     string
	JsonData string
}

func processModel(req *dash.PanelRequest, model *PlaygroundModel) {
	req.SetData("$.html", model.Html)

	var dynData interface{}
	err := json.Unmarshal([]byte(model.JsonData), &dynData)
	if err != nil {
		req.SetData("$.data", nil)
		req.SetData("$.submiterrors", err.Error())
	} else {
		req.SetData("$.data", dynData)
	}
}

func main() {
	cfg := &dash.Config{ProcName: "demo2", AnonAcc: true, AutoKeygen: true}
	dash.StartProcClient(cfg)
	defer dash.WaitForClear()
	dash.RegisterPanelHandler("playground", "/", func(req *dash.PanelRequest) error {
		req.NoAuth()
		req.SetHtmlFromFile("cmd/playground/playground.html")
		html := strings.TrimSpace(INITIAL_HTML)
		jsonData := strings.TrimSpace(INITIAL_DATA)
		req.SetData("$.model.html", html)
		req.SetData("$.model.jsondata", jsonData)
		processModel(req, &PlaygroundModel{Html: html, JsonData: jsonData})
		return nil
	})
	dash.RegisterPanelHandler("playground", "/submit", func(req *dash.PanelRequest) error {
		var model PlaygroundModel
		mapstructure.Decode(req.Model, &model)
		processModel(req, &model)
		return nil
	})
	select {}
}
