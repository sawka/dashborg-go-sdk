package main

import (
	"fmt"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func main() {
	cfg := &dash.Config{ProcName: "hello", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()
	pw := dash.DefinePanel("button-test")
	pw.Print("[@h1] Button Test")
	btn := pw.Print("<button/> Run Backend Code")
	pw.Flush()
	btn.OnClick(func() error {
		fmt.Printf("Running Backend Code...\n")
		return nil
	})
	select {}
}
