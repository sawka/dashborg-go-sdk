package main

import "github.com/sawka/dashborg-go-sdk/pkg/dash"

func main() {
	config := &dash.Config{}
	defer dash.StartProcClient(config).WaitForClear()
	pw := dash.DefinePanel("text")
	pw.Print("[@h1] Header (H1)")
	pw.Print("[@h2] Header (H2)")
	pw.Print("[@h3] Header (H3)")
	pw.Print("[@h4] Header (H4)")
	pw.Flush()
}
