package main

import (
	"bytes"       // turns the built-in font file into something we can read
	"fmt"         // fmt.Sprintf for the status line
	"image/color" // colour types
	"log"         // log.Fatal on errors

	"github.com/hajimehoshi/ebiten/v2"           // the engine: window and game loop
	"github.com/hajimehoshi/ebiten/v2/inpututil" // "was this key just pressed?"
	"github.com/hajimehoshi/ebiten/v2/text/v2"   // drawing text
	"github.com/hajimehoshi/ebiten/v2/vector"    // drawing circles and rectangles
	"golang.org/x/image/font/gofont/goregular"   // the Go Regular font, built into the library
)

// Colours we can draw agents with (red, green, blue, opacity – each 0x00 to 0xff).
var (
	Green  = color.RGBA{0x00, 0x68, 0x37, 0xff}
	Orange = color.RGBA{0xf4, 0xa5, 0x24, 0xff}
	Red    = color.RGBA{0xa5, 0x00, 0x26, 0xff}
	Blue   = color.RGBA{0x45, 0x75, 0xb4, 0xff}
)

// LegendItem is one coloured marker plus label shown above the plot.
type LegendItem struct {
	Label  string      // the text, e.g. "Susceptible=315"
	Colour color.Color // colour of the marker dot next to it
}

// Frame is one picture: where each agent is, what colour to draw it, and an optional legend.
type Frame struct {
	X, Y   []float64     // position of every agent
	Colour []color.Color // colour of every agent
	Legend []LegendItem  // labels drawn above the plot
}

// Layout of the window, in pixels.
const (
	windowSize   = 600 // the window is 600x600
	plotLeft     = 75  // left edge of the plot area
	plotRight    = 540 // right edge of the plot area
	plotTop      = 72  // top edge of the plot area
	plotBottom   = 534 // bottom edge of the plot area
	markerRadius = 4   // radius of each agent's dot
)

const stepsPerSecond = 10 // how many simulation steps to run per second

// Game holds everything the window needs between frames.
type Game struct {
	agents []Agent          // the simulation's agents
	steps  int              // how many steps have run
	paused bool             // true while paused
	font   *text.GoTextFace // font for the legend and status line
}

// Update is called stepsPerSecond times a second: handle keys, then advance the simulation one step.
func (g *Game) Update() error {
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.paused = !g.paused // toggle pause on/off
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyR) { // R was pressed…
		g.agents = newAgents(numAgents) //start again with fresh agents
		g.steps = 0                     // reset the step counter
	}
	if !g.paused { // not paused
		step(g.agents) // move the simulation forward one tick
		g.steps++      // count the step
	}
	return nil // nil = no error, keep running
}

// Draw paints the current state of the simulation. Ebitengine calls it every screen refresh.
func (g *Game) Draw(screen *ebiten.Image) {
	f := snapshot(g.agents)  // positions, colours and legend from main.go
	screen.Fill(color.White) // clear to a white background

	// black 1-pixel border around the plot area
	vector.StrokeRect(screen, plotLeft, plotTop, plotRight-plotLeft, plotBottom-plotTop, 1, color.Black, false)

	// keep dots a few pixels inside the border
	const pad = markerRadius + 4
	// how many pixels one world unit takes up, sideways (sx) and up/down (sy)
	sx := float64(plotRight-plotLeft-2*pad) / (xBounds[1] - xBounds[0])
	sy := float64(plotBottom-plotTop-2*pad) / (yBounds[1] - yBounds[0])

	// draw every agent
	for i := range f.X {
		px := plotLeft + pad + (f.X[i]-xBounds[0])*sx   // world x → pixel x
		py := plotBottom - pad - (f.Y[i]-yBounds[0])*sy // world y → pixel y (flipped: screens count y downwards)
		// filled dot in the agent's colour; true = smooth edges
		vector.FillCircle(screen, float32(px), float32(py), markerRadius, f.Colour[i], true)
	}

	// legend in a 2x2 grid above the plot
	colX := [2]float64{plotLeft, (plotLeft + plotRight) / 2} // x of the left and right columns
	for i, item := range f.Legend {
		x := colX[i%2]            // even i → left column, odd i → right column
		y := 12 + float64(i/2)*26 // first row at y=12, second row 26 px lower
		// coloured dot, then the label to the right of it
		vector.FillCircle(screen, float32(x+markerRadius), float32(y+11), markerRadius, item.Colour, true)
		g.drawText(screen, item.Label, x+2*markerRadius+6, y)
	}

	status := fmt.Sprintf("Step %d   Space: pause   R: restart", g.steps) // status line under the plot
	if g.paused {                                                         // different text while paused
		status = fmt.Sprintf("Step %d   PAUSED   Space: resume   R: restart", g.steps)
	}
	g.drawText(screen, status, plotLeft, plotBottom+16) // draw it below the plot
}

// drawText writes s in black with its top-left corner at (x, y).
func (g *Game) drawText(screen *ebiten.Image, s string, x, y float64) {
	op := &text.DrawOptions{}                 // drawing options
	op.GeoM.Translate(x, y)                   // move the text to (x, y)
	op.ColorScale.ScaleWithColor(color.Black) // make it black
	text.Draw(screen, s, g.font, op)          // draw it
}

// Layout tells Ebitengine how big the drawing area is; the window scales it to fit.
func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return windowSize, windowSize // always draw at 600x600
}

// runLive opens a window and runs the simulation in it until the window is closed.
func runLive() {
	// load the built-in font
	src, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil { // couldn't load it
		log.Fatal(err) // print the error and stop
	}

	ebiten.SetWindowSize(windowSize, windowSize)                   // open a 600x600 window
	ebiten.SetWindowTitle("Disease simulation")                    // text in the title bar
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled) // allow resizing (the picture scales)
	ebiten.SetTPS(stepsPerSecond)                                  // call Update 10 times a second

	game := &Game{
		agents: newAgents(numAgents),                    // fresh agents
		font:   &text.GoTextFace{Source: src, Size: 18}, // the font at 18 px
	}
	if err := ebiten.RunGame(game); err != nil { // open the window and run until it's closed
		log.Fatal(err) // if something went wrong, print it and stop
	}
}
