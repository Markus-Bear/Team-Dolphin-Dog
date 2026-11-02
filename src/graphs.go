// The graphs beside the map: how the outbreak has gone, day by day.
package main

import (
	"fmt"         // labels
	"image/color" // colours

	"github.com/hajimehoshi/ebiten/v2"        // the window
	"github.com/hajimehoshi/ebiten/v2/vector" // lines and rectangles
)

// Where the graphs go: a column to the right of the map, as tall as the map.
const (
	graphLeft  = plotLeft + plotWidth + 30 // left edge of every graph
	graphWidth = 330
	graphGap   = 46 // room above each graph for its title
)

var (
	graphFrame = color.RGBA{0x99, 0x99, 0x99, 0xff} // the box round each graph
	stageLine  = color.RGBA{0x00, 0x00, 0x00, 0x60} // see-through black: when each response stage began
	sealedLine = SealedRed                          // counties sealed
)

// line is one line on a graph.
type line struct {
	colour color.Color
	value  func(day int) float64 // its value on a day, in real people (or counties)
}

// graph is one box: a title, its lines, and the number at the top of its scale.
type graph struct {
	title string
	lines []line
	top   float64 // the value at the top of the box; 0 = the largest value so far
	unit  string  // after the number at the top, e.g. " people"
}

// graphs lists the four graphs.
func (g *Game) graphs() []graph {
	w := g.world
	people := func(s State) func(int) float64 { // a state's count on a day, in real people
		return func(day int) float64 { return float64(w.History[day][s]) * w.PeoplePerDot() }
	}

	everyone := graph{title: "Everyone", top: w.Map.TotalPeople, unit: " people"} // every state, on a scale of the whole island
	for s := range numStates {
		everyone.lines = append(everyone.lines, line{stateColours[s], people(s)})
	}

	sick := graph{title: "Caught it or ill now", unit: " people"} // only the sick, so small outbreaks still show
	for _, s := range []State{ExposedBubonic, ExposedPneumonic, Bubonic, Pneumonic} {
		sick.lines = append(sick.lines, line{stateColours[s], people(s)})
	}
	if w.Era.Modern && isolateDiagnosed { // a black line for hospital patients, as in Team-Dolphin-Dog
		sick.title += " (black = in hospital)"
		sick.lines = append(sick.lines, line{color.Black, func(day int) float64 { return float64(w.IsoHist[day][0]) * w.PeoplePerDot() }})
	}

	deaths := graph{title: "Deaths per day (7-day average)", unit: " people", lines: []line{{Grey, func(day int) float64 {
		from := max(day-7, 0) // a week back, or day 0; averaged so the 35-people steps don't make it jump
		return float64(w.History[day][Dead]-w.History[from][Dead]) * w.PeoplePerDot() / float64(max(day-from, 1))
	}}}}

	sealed := graph{title: "Counties sealed", top: float64(len(w.Map.Counties) - 1), unit: " counties", lines: []line{{sealedLine, func(day int) float64 {
		return float64(w.SealedHist[day])
	}}}}
	return []graph{everyone, sick, deaths, sealed}
}

// drawGraphs draws the four graphs in a column beside the map.
func (g *Game) drawGraphs(screen *ebiten.Image) {
	list := g.graphs()
	height := float32(plotHeight)/float32(len(list)) - graphGap // each box's height
	days := len(g.world.History)
	for i, gr := range list {
		left := float32(graphLeft)
		top := float32(plotTop) + float32(i)*(height+graphGap) + graphGap - 8
		right, bottom := left+graphWidth, top+height

		maxValue := gr.top
		if maxValue == 0 { // scale to the largest value so far
			for _, l := range gr.lines {
				for day := range days {
					maxValue = max(maxValue, l.value(day))
				}
			}
			maxValue = max(maxValue, 1) // an empty graph still has a scale
		}

		g.drawText(screen, gr.title, float64(left), float64(top)-40, color.Black)
		g.drawTextWith(screen, g.smallFont, thousands(maxValue)+gr.unit, float64(left), float64(top)-18, color.RGBA{0x55, 0x55, 0x55, 0xff}) // the top of the scale
		g.drawTextWith(screen, g.smallFont, fmt.Sprintf("day %d", g.world.Day), float64(right)-40, float64(bottom)+2, color.RGBA{0x55, 0x55, 0x55, 0xff})
		vector.StrokeRect(screen, left, top, graphWidth, height, 1, graphFrame, false)

		if gr.title == "Counties sealed" && !g.world.Era.Modern {
			g.drawText(screen, "No lockdowns in 1348", float64(left)+90, float64(top+height/2)-10, color.RGBA{0x55, 0x55, 0x55, 0xff})
			continue
		}

		toScreen := func(day int, v float64) (float32, float32) { // a day and value to a point in the box
			x := left + graphWidth*float32(day)/float32(max(days-1, 1))
			y := bottom - height*float32(min(v/maxValue, 1))
			return x, y
		}

		for s := FirstCase; s < numStages; s++ { // a faint upright line on the day each response stage began
			if g.world.Stage >= s {
				x, _ := toScreen(g.world.StageDay[s], 0)
				vector.StrokeLine(screen, x, top, x, bottom, 1, stageLine, false)
			}
		}

		stride := max(days/graphWidth, 1) // at most one point per pixel across
		for _, l := range gr.lines {
			px, py := toScreen(0, l.value(0))
			for day := stride; day < days; day += stride {
				x, y := toScreen(day, l.value(day))
				vector.StrokeLine(screen, px, py, x, y, 2, l.colour, true)
				px, py = x, y
			}
		}
	}
}
