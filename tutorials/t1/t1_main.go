package main

import (
	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

func main() {
	cfg := &dash.Config{ProcName: "hello", AnonAcc: true, AutoKeygen: true}
	defer dash.StartProcClient(cfg).WaitForClear()
	pw := dash.DefinePanel("default")
	pw.Print("[@h1] Hello World")
	pw.Flush()
}
