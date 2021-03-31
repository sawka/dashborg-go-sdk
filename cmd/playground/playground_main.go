package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

type PreSet struct {
	Html string
	Data map[string]interface{}
}

type MSI = map[string]interface{}

var PRESET_MAP map[string]*PreSet

func init() {

	m := make(map[string]*PreSet)

	messagePS := &PreSet{
		Html: `
<h1>Messages</h1>

<d-message title="Basic Message">
    This is some message content in a basic message block.
</d-message>

<d-message title="Message Classes" class="*$.data.messagetype">
    This content is in a <d-text bind="$.data.messagetype"/> message block.
    Here are some other options (select to try):
    <d-select bindvalue="$.data.messagetype" style="max-width: 200px;">
        <d-option>success</d-option>
        <d-option>info</d-option>
        <d-option>warning</d-option>
        <d-option>error</d-option>
    </d-select>
</d-message>
`,
		Data: MSI{"messagetype": "success"},
	}
	m["message"] = messagePS

	tablePS := &PreSet{
		Html: `
<h1>Tables</h1>
<div class="row">
   <d-button handler="/add-row">Add Row</d-button>
</div>
<d-table bind="$.data.tabledata" keyexpr="@index">
   <d-col label="Row#">
     <d-text bind="@index"/>
   </d-col>
   <d-col label="X">
     <d-text bind=".x"/>
   </d-col>
   <d-col label="Y">
     <d-text bind=".y"/>
   </d-col>
   <d-col label="X + Y">
     <d-text bind=".x + .y"/>
   </d-col>
   <d-col label="Type">
     <d-text bind="(.x + .y) % 2 == 0 ? 'even' : 'odd'"/>
   </d-col>
   <d-col label="Actions">
     <div class="row">
       <d-button handler="/add-data({row: @index, key: 'x', val: 1})">X+1</d-button>
       <d-button handler="/add-data({row: @index, key: 'x', val: -1})">X-1</d-button>
       <d-button handler="/add-data({row: @index, key: 'y', val: 1})">Y+1</d-button>
       <d-button handler="/add-data({row: @index, key: 'y', val: -1})">Y-1</d-button>
     </div>
   </d-col>
</d-table>
`,
		Data: MSI{"tabledata": []MSI{
			MSI{"x": 55, "y": 2},
			MSI{"x": 28, "y": 100},
			MSI{"x": 710, "y": -5},
			MSI{"x": 34, "y": 38},
		}},
	}
	m["table"] = tablePS

	PRESET_MAP = m
}

const INITIAL_HTML = `
<h1>Test Output</h1>
<div class="row" style="xcenter;">
  <d-button>Test Button</d-button>
  <div style="color: *$.data.color;">
    <d-text bind="$.data.text"/>
  </div>
</div>
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

func PreSetHandler(req *dash.PanelRequest) error {
	if req.Data == nil {
		return fmt.Errorf("Invalid Preset (nil)")
	}
	presetType, ok := req.Data.(string)
	if !ok || PRESET_MAP[presetType] == nil {
		return fmt.Errorf("Invalid Preset")
	}
	preset := PRESET_MAP[presetType]
	req.SetData("$.html", strings.TrimSpace(preset.Html))
	req.SetData("$state.html", strings.TrimSpace(preset.Html))
	req.SetData("$.data", preset.Data)
	req.SetData("$state.preset", presetType)
	barr, err := json.MarshalIndent(preset.Data, "", "  ")
	if err != nil {
		return err
	}
	req.SetData("$state.jsondata", string(barr))
	return nil
}

func main() {
	cfg := &dash.Config{ProcName: "demo2", AnonAcc: true, AutoKeygen: true}
	dash.StartProcClient(cfg)
	defer dash.WaitForClear()
	dash.RegisterPanelHandler("playground", "/", func(req *dash.PanelRequest) error {
		req.SetHtmlFromFile("cmd/playground/playground.html")
		req.Data = "table"
		PreSetHandler(req)
		return nil
	})
	dash.RegisterPanelHandler("playground", "/submit", func(req *dash.PanelRequest) error {
		var model PlaygroundModel
		mapstructure.Decode(req.PanelState, &model)
		processModel(req, &model)
		return nil
	})
	dash.RegisterPanelHandler("playground", "/preset", PreSetHandler)

	type addDataParams struct {
		Row int    `json:"row"`
		Key string `json:"key"`
		Val int    `json:"val"`
	}
	dash.RegisterPanelHandlerEx("playground", "/add-data", func(req *dash.PanelRequest, state interface{}, data addDataParams) error {
		preset := PRESET_MAP["table"]
		rows := preset.Data["tabledata"].([]MSI)
		curVal := rows[data.Row][data.Key].(int)
		rows[data.Row][data.Key] = curVal + data.Val
		req.SetData("$.data", preset.Data)
		barr, err := json.MarshalIndent(preset.Data, "", "  ")
		if err != nil {
			return err
		}
		req.SetData("$state.jsondata", string(barr))
		return nil
	})
	dash.RegisterPanelHandler("playground", "/add-row", func(req *dash.PanelRequest) error {
		preset := PRESET_MAP["table"]
		rows := preset.Data["tabledata"].([]MSI)
		rows = append(rows, MSI{"x": 0, "y": 0})
		preset.Data["tabledata"] = rows
		req.SetData("$.data", preset.Data)
		barr, err := json.MarshalIndent(preset.Data, "", "  ")
		if err != nil {
			return err
		}
		req.SetData("$state.jsondata", string(barr))
		return nil
	})
	select {}
}
