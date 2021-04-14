package main

import (
	"regexp"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func RootHandler(req *dash.PanelRequest) error {
	err := req.SetHtmlFromFile("cmd/bulma/bulma.html")
	if err != nil {
		return err
	}
	return nil
}

func TestClick(req *dash.PanelRequest) error {
	req.SetData("$.clicked", true)
	return nil
}

var ZIP_RE = regexp.MustCompile("^[0-9]{5}$")

func ClickZipCode(req *dash.PanelRequest, model interface{}, data string) error {
	if data == "" || !ZIP_RE.MatchString(data) {
		req.SetData("$.ziperror", "Please Enter a Valid Zip Code")
		return nil
	}
	req.SetData("append:$.tabledata", data)
	req.SetData("$state.zipcode", "")
	return nil
}

func main() {
	config := &dash.Config{AutoKeygen: true, AnonAcc: true}
	dash.StartProcClient(config)
	defer dash.WaitForClear()

	dash.RegisterPanelHandler("bulma", "/", RootHandler)
	dash.RegisterPanelHandler("bulma", "/test-click", TestClick)
	dash.RegisterPanelHandlerEx("bulma", "/test-zip", ClickZipCode)

	select {}
}
