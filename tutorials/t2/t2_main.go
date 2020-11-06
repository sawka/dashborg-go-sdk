package main

import (
	"fmt"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func main() {
	cfg := &dash.Config{ProcName: "hello", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()
	pw := dash.DefinePanel("default")
	pw.Print("[@h1] Hello World")
	btn := pw.Print("<button/> Click Me!")
	pw.Flush()
	btn.OnClick(func() error {
		fmt.Printf("Button Clicked\n")
		return nil
	})
	select {} // block forever
}
