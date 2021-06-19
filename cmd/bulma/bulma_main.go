package main

import (
	"regexp"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
	"github.com/sawka/dashborg-go-sdk/pkg/dashcloud"
)

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
	req.SetData("$.ziperror", nil)
	req.SetData("append:$.tabledata", data)
	req.SetData("$state.zipcode", "")
	return nil
}

func main() {
	app := dash.MakeApp("bulma")
	app.SetHtmlFromFile("cmd/bulma/bulma.html")
	app.Handler("/test-click", TestClick)
	app.HandlerEx("/test-zip", ClickZipCode)

	config := &dash.Config{AutoKeygen: true, AnonAcc: true}
	container, _ := dashcloud.MakeClient(config)
	container.ConnectApp(app)

	select {}
}
