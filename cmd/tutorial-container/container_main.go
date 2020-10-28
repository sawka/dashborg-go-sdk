package main

import "github.com/sawka/dashborg-go-sdk/pkg/dash"

func main() {
	config := &dash.Config{AnonAcc: true, ProcName: "tutorialcontainer"}
	defer dash.StartProcClient(config).WaitForClear()
	pw := dash.DefinePanel("container")
	pw.Print("[@h1] Container Tutorial")
	pw.Print("<div>[col:natural @maxwidth=700px @bgcolor=#e2e7ea @padding=10px @radius=5px]")
	pw.Print("Dashborg layout is based on the CSS FlexBox model.")
	pw.Print("<link/>[@href='https://css-tricks.com/snippets/css/a-guide-to-flexbox/'] https://css-tricks.com/snippets/css/a-guide-to-flexbox/")
	pw.Print("The attributes which control containers in Dashborg are @jc/@justifycontent, @alignitems, @aligncontent, @center, @xcenter, @wrap, @margin(top/bottom/right/left), @padding(top/bottom/right/left), @border(top/bottom/right/left), @width/@minwidth/@maxwidth, @height/@minheight/@maxheight, @radius, @scroll, @overflow(x/y), @grow, @shrink, @row, and @col.  These generally correspond directly to their CSS equivalents.")
	pw.Print("There are also special classes which can help to style divs, including 'row', 'col', and 'natural'.")
	pw.Print("</div>")

	pw.Print("[@maxwidth=700px] In Dashborg the <div> element is used to group other elements for display.  It is common to want to group elements in rows or columns.")

	pw.Print("[@h2] Rows")
	pw.Print("[@maxwidth=700px] Here are some Dashborg row examples.  You can create row by writing \"<div>[row]\" (a div element with a classname of 'row').  Other attributes, like background-color, padding/margin, height, width, etc. can always be added as well.  By default a row will have width of 100% and place 10px of padding between elements.  If you don't want your row to take up 100% width, you can specify a different width or use the class 'natural' to have it fit the contents (e.g. \"<div>[row:natural]\").")

	// row example, useful for buttons
	// using @notrackactive to not have the buttons show as disabled when the program is not running.
	pw.Print("[@bold] Simple Row of Buttons")
	pw.Print("<div>[row]")
	pw.Print("<button/>[@uiprimary @notrackactive] Button #1")
	pw.Print("<button/>[@notrackactive] Button #2")
	pw.Print("<button/>[@notrackactive] Button With Long Name")
	pw.Print("<button/>[@notrackactive] B4")
	pw.Print("</div>")

	// showing a row with extra padding and showing alignment properties
	// @jc=center (justify-content) will center elements, can also be done using @center
	pw.Print("[@bold] Aligning items vertically.")
	pw.Print("<div>[row @height=100px @bgcolor=#96ADC8 @jc=center @shrink=0 @padding=5px]")
	pw.Print("<button/>[@uiprimary @notrackactive @paddingtop=10px] Button #1")
	pw.Print("<button/>[@notrackactive @alignself=flex-end] Button #2")
	pw.Print("<button/>[@notrackactive @alignself=flex-start] Button With Long Name")
	pw.Print("<button/>[@notrackactive @alignself=center] B4")
	pw.Print("</div>")

	// a row creates a 16-element pseudo grid.  using the classes "s1".."s16" you can size items.
	pw.Print("[@bold] A row creates a 16-element pseudo-grid.")
	pw.Print("<div>[row @height=50px @bgcolor=#96ADC8 @paddingtop=5px @paddingbottom=5px @shrink=0]")
	pw.Print("[s2 @bgcolor=#DB7F8E @fullcenter] S2")
	pw.Print("[s1 @bgcolor=#2C4251 @color=white @fullcenter] S1")
	pw.Print("[s1 @bgcolor=#2C4251 @color=white @fullcenter] S1")
	pw.Print("[s7 @bgcolor=#D2D7DA @fullcenter] S7")
	pw.Print("[s7 @bgcolor=#DB7F8E @fullcenter] S5")
	pw.Print("</div>")

	// space evenly
	pw.Print("[@bold] @jc=space-evenly")
	pw.Print("<div>[row @height=50px @bgcolor=#96ADC8 @paddingtop=5px @paddingbottom=5px @jc=space-evenly @shrink=0]")
	pw.Print("[s1 @bgcolor=#2C4251 @color=white @fullcenter] S1")
	pw.Print("[s1 @bgcolor=#2C4251 @color=white @fullcenter] S1")
	pw.Print("[s1 @bgcolor=#2C4251 @color=white @fullcenter] S1")
	pw.Print("</div>")

	pw.Print("[@h2 @margintop=20px] Columns")
	pw.Print("Columns work the same as rows, except use \"<div>[col]\".  They are always 100% height unless otherwise specified, or unless they use the 'natural' class.")
	pw.Print("<div>[row @shrink=0 @height=500px]") // to show the column examples side by side

	pw.Print("<div>[s2:col @borderright='1px solid black' @paddingright=10px]")
	pw.Print("[@bold] Column of Buttons")
	pw.Print("<button/>[@notrackactive @uiprimary] B1")
	pw.Print("<button/>[@notrackactive] B2")
	pw.Print("<button/>[@notrackactive] B3")
	pw.Print("</div>")

	pw.Print("<div>[s3:col @borderright='1px solid black' @paddingright=10px @paddingleft=10px @jc=center]")
	pw.Print("[@bold] Alignment")
	pw.Print("<button/>[@notrackactive @uiprimary @width=50%] B1")
	pw.Print("<button/>[@notrackactive] B2")
	pw.Print("<button/>[@notrackactive @width=66% @alignself=flex-end] B3")
	pw.Print("<button/>[@notrackactive @width=66% @alignself=center] B4")
	pw.Print("</div>")

	pw.Print("<div>[s3:col @borderright='1px solid black' @paddingright=10px @paddingleft=10px]")
	pw.Print("[s1 @bold] 16-Elem Pseudo Grid (S1)")
	pw.Print("[s2 @bgcolor=#DB7F8E @fullcenter] S2")
	pw.Print("[s4 @bgcolor=#D2D7DA @fullcenter] S4")
	pw.Print("[s6 @bgcolor=#2C4251 @color=white @fullcenter] S6")
	pw.Print("[s3 @bgcolor=#7EBC89 @color=white @fullcenter] S3")
	pw.Print("</div>")

	pw.Print("<div>[s2:col @borderright='1px solid black' @paddingright=10px @jc=space-evenly]")
	pw.Print("[@bold] Space Evenly")
	pw.Print("<button/>[@notrackactive @uiprimary] B1")
	pw.Print("<button/>[@notrackactive] B2")
	pw.Print("<button/>[@notrackactive] B3")
	pw.Print("</div>")

	pw.Print("</div>")

	pw.Print("[@height=100px @shrink=0]") // bottom spacer

	pw.Flush()
}
