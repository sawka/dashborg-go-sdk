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
	pw.Print("[@h5] Header (H5)")
	pw.Print("[@h6] Header (H6)")
	pw.Print("Normal Text")
	pw.Print("[@color=blue] Text can have a blue color (@color)")
	pw.Print("[@color=#ff3333] Text can have a hex color (#ff3333)")
	pw.Print("[@color=rgba(200,200,200,0.8)] Text can have a rgba color (rgba(200,200,200,0.8))")
	pw.Print("[@bold] Text can be bold (@bold)")
	pw.Print("[@underline] Text can be underlined (@underline)")
	pw.Print("[@italic] Text can be italic (@italic)")
	pw.Print("[@size=8px] Text can specify a small font size, like 8px (@size)")
	pw.Print("[@size=22px] Text can specify a large font size, like 22px")
	pw.Print("[@strike] Text can be strikethrough (@strike)")
	pw.Print("[@fixedfont] Text can be fixed-wdith (@fixedfont)")
	pw.Print("[@bgcolor=#ccffcc] Text is a block element, so it can have a green background color (@bgcolor)")
	pw.Print("[@border='1px solid black' @width=50%] Text can have a border and a width (@border, @width)")
	pw.Print("[@bgcolor=#ccccff @padding=10px @width=50% @border='3px solid #777'] Text can have padding (and width and a border)")
	pw.Print("[@bgcolor=#e0e0ff @padding=10px @width=50% @radius=5px] You can round corners with @radius")
	pw.Print("[@bgcolor=#ccc @paddingtop=20px @paddingleft=20px @paddingbottom=5px @paddingright=2px] Top/Bottom/Left/Right padding is supported as separate attributes")
	pw.Print("[@bgcolor=#ccc @padding='20px 2px 5px 20px'] Or as 1-attribute (top, right, bottom, left), like CSS")
	pw.Print("[@bgcolor=#333 @color=white @margin=10px] Margin is supported in the same way as padding")
	pw.Print("[@bgcolor=#ccc @paddingleft=10% @paddingright=10% @width=50%] margin, padding, width, and height can be percentages")
	pw.Print("[@height=40px @shrink=0 @bgcolor=#ccc @jc=center @alignitems=center] Text can be aligned @jc / @justifycontent and @alignitems, centered")
	pw.Print("[@height=40px @shrink=0 @bgcolor=#ccc @center @xcenter] Centering can also be done with @center / @xcenter for main and cross-axis centering")
	pw.Flush()
}
