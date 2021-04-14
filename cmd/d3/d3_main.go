package main

import (
	"math/rand"
	"time"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

type DataPoint struct {
	X     int
	Y     int
	Val   int
	Color string
}

const NUM_DATA = 30

var Colors []string = []string{"red", "green", "blue", "purple"}

func RootHandler(req *dash.PanelRequest) error {
	err := req.SetHtmlFromFile("cmd/d3/d3-test.html")
	if err != nil {
		return err
	}
	return RegenData(req)
}

func RegenData(req *dash.PanelRequest) error {
	rtn := make([]DataPoint, 0)
	for i := 0; i < NUM_DATA; i++ {
		point := DataPoint{
			X:     rand.Intn(50),
			Y:     rand.Intn(50),
			Val:   rand.Intn(50) + 1,
			Color: Colors[rand.Intn(len(Colors))],
		}
		rtn = append(rtn, point)
	}
	req.SetData("$.data", rtn)
	return nil
}

func main() {
	rand.Seed(time.Now().Unix())
	config := &dash.Config{AutoKeygen: true, AnonAcc: true}
	dash.StartProcClient(config)
	defer dash.WaitForClear()

	dash.RegisterPanelHandler("d3-test", "/", RootHandler)
	dash.RegisterPanelHandler("d3-test", "/regen-data", RegenData)

	select {}

}
