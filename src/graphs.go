// The graphs beside the map: how the outbreak has gone, day by day.
package main

import (
	"fmt"         // labels
	"image/color" // colours
	"math"        // rounding the scale
	"sort"        // putting today's numbers in order, top to bottom
	"strconv"     // writing numbers

	"github.com/hajimehoshi/ebiten/v2"         // the window
	"github.com/hajimehoshi/ebiten/v2/text/v2" // measuring labels
	"github.com/hajimehoshi/ebiten/v2/vector"  // lines and rectangles
)

// Where the graphs go: a column to the right of the map, from the top of the map to the bottom of
// the window. Each graph gets one slot: its title, the box, the day numbers under it, then a gap.
const (
	graphLeft     = plotLeft + plotWidth + 70 // left edge of every box; the scale numbers go to its left
	graphWidth    = 300
	graphSlot     = (windowHeight - plotTop - 12) / 4 // height of one graph's slot (4 graphs)
	graphTitle    = 26                                // room above each box for its title
	graphUnder    = 18                                // room under each box for the day numbers
	graphSpace    = 22                                // empty space between one graph and the next
	graphHeight   = graphSlot - graphTitle - graphUnder - graphSpace
	graphValueGap = 13 // the smallest distance between two numbers at the right of a box
)

var (
	graphFrame = color.RGBA{0x99, 0x99, 0x99, 0xff} // the box round each graph
	stageLine  = color.RGBA{0x00, 0x00, 0x00, 0x60} // see-through black: when each response stage began
	gridLine   = color.RGBA{0x00, 0x00, 0x00, 0x18} // very faint black: the middle of the scale
	scaleGrey  = color.RGBA{0x55, 0x55, 0x55, 0xff} // the numbers on the scales
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
}

// graphs lists the four graphs.
func (g *Game) graphs() []graph {
	w := g.world
	people := func(s State) func(int) float64 { // a state's count on a day, in real people
		return func(day int) float64 { return float64(w.History[day][s]) * w.PeoplePerDot() }
	}

	everyone := graph{title: "Everyone", top: w.Map.TotalPeople} // every state, on a scale of the whole island
	for s := range numStates {
		everyone.lines = append(everyone.lines, line{stateColours[s], people(s)})
	}

	sick := graph{title: "Caught it or ill now"} // only the sick, so small outbreaks still show
	for _, s := range []State{ExposedBubonic, ExposedPneumonic, Bubonic, Pneumonic} {
		sick.lines = append(sick.lines, line{stateColours[s], people(s)})
	}
	if w.Era.Modern && isolateDiagnosed { // a black line for hospital patients, as in Team-Dolphin-Dog
		sick.title += " (black = in hospital)"
		sick.lines = append(sick.lines, line{color.Black, func(day int) float64 { return float64(w.IsoHist[day][0]) * w.PeoplePerDot() }})
	}

	deaths := graph{title: "Deaths per day (7-day average)", lines: []line{{Grey, func(day int) float64 {
		from := max(day-7, 0) // a week back, or day 0; averaged so the 35-people steps don't make it jump
		return float64(w.History[day][Dead]-w.History[from][Dead]) * w.PeoplePerDot() / float64(max(day-from, 1))
	}}}}

	sealed := graph{title: "Counties sealed", top: float64(len(w.Map.Counties) - 1), lines: []line{{sealedLine, func(day int) float64 {
		return float64(w.SealedHist[day])
	}}}}
	return []graph{everyone, sick, deaths, sealed}
}

// drawGraphs draws the four graphs in a column beside the map. Each box has its scale on the left
// (0, half way and the top), the days underneath, and today's value of each line on the right.
func (g *Game) drawGraphs(screen *ebiten.Image) {
	days := len(g.world.History)
	for i, gr := range g.graphs() {
		left := float32(graphLeft)
		top := float32(plotTop + i*graphSlot + graphTitle)
		right, bottom := left+graphWidth, top+graphHeight

		maxValue := gr.top
		if maxValue == 0 { // scale to the largest value so far
			for _, l := range gr.lines {
				for day := range days {
					maxValue = max(maxValue, l.value(day))
				}
			}
			maxValue = niceTop(max(maxValue, 1)) // round up, so the scale has easy numbers
		}

		g.drawText(screen, gr.title, float64(left)-50, float64(top)-24, color.Black)
		vector.StrokeLine(screen, left, top+graphHeight/2, right, top+graphHeight/2, 1, gridLine, false) // half way up
		vector.StrokeRect(screen, left, top, graphWidth, graphHeight, 1, graphFrame, false)

		// The scale on the left: the top, half way, and 0.
		for _, f := range []float32{1, 0.5, 0} {
			label := short(maxValue * float64(f))
			w, _ := text.Measure(label, g.smallFont, 0)
			g.drawTextWith(screen, g.smallFont, label, float64(left)-6-w, float64(bottom-graphHeight*f)-7, scaleGrey)
		}

		// The days underneath: day 0, half way, and today.
		for _, f := range []float32{0, 0.5, 1} {
			label := fmt.Sprintf("%d", int(float32(days-1)*f+0.5))
			if f == 0 {
				label = "day 0"
			}
			w, _ := text.Measure(label, g.smallFont, 0)
			x := float64(left+graphWidth*f) - w*float64(f) // left-aligned at 0, centred half way, right-aligned at today
			g.drawTextWith(screen, g.smallFont, label, x, float64(bottom)+2, scaleGrey)
		}

		if gr.title == "Counties sealed" && !g.world.Era.Modern {
			g.drawText(screen, "No lockdowns in 1348", float64(left)+75, float64(top+graphHeight/2)-10, scaleGrey)
			continue
		}

		toScreen := func(day int, v float64) (float32, float32) { // a day and value to a point in the box
			x := left + graphWidth*float32(day)/float32(max(days-1, 1))
			y := bottom - graphHeight*float32(min(v/maxValue, 1))
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

		g.drawTodayValues(screen, gr, toScreen, days-1, right, top, bottom)
	}
}

// drawTodayValues writes today's value of each line just right of the box, in the line's colour,
// level with the end of the line. Lines at 0 are left out (unless the graph has only one line), and
// numbers that would overlap are moved apart.
func (g *Game) drawTodayValues(screen *ebiten.Image, gr graph, toScreen func(int, float64) (float32, float32), today int, right, top, bottom float32) {
	type label struct {
		text   string
		colour color.Color
		y      float32
	}
	var labels []label
	for _, l := range gr.lines {
		v := l.value(today)
		if math.Round(v) <= 0 && len(gr.lines) > 1 {
			continue
		}
		_, y := toScreen(today, v)
		labels = append(labels, label{short(v), l.colour, y - 7})
	}
	sort.Slice(labels, func(a, b int) bool { return labels[a].y < labels[b].y })
	for i := range labels { // push each number down until it clears the one above
		labels[i].y = max(labels[i].y, top-7)
		if i > 0 {
			labels[i].y = max(labels[i].y, labels[i-1].y+graphValueGap)
		}
	}
	for i := len(labels) - 1; i >= 0; i-- { // then up again if that pushed them past the bottom
		limit := bottom - 7
		if i < len(labels)-1 {
			limit = labels[i+1].y - graphValueGap
		}
		labels[i].y = min(labels[i].y, limit)
	}
	for _, lb := range labels {
		g.drawTextWith(screen, g.smallFont, lb.text, float64(right)+5, float64(lb.y), lb.colour)
	}
}

// niceTop rounds v up to 1, 2 or 5 times a power of ten (or 2.5 or 7.5), so the scale's numbers are easy to read.
func niceTop(v float64) float64 {
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, f := range []float64{1, 2, 2.5, 5, 7.5, 10} {
		if f*p >= v {
			return f * p
		}
	}
	return 10 * p
}

// short writes a number briefly: 950, 12,400, 125k, 7.08M.
func short(v float64) string {
	switch {
	case v >= 1e6:
		return strconv.FormatFloat(v/1e6, 'f', 2, 64) + "M"
	case v >= 1e5:
		return strconv.FormatFloat(v/1e3, 'f', 0, 64) + "k"
	default:
		return thousands(v)
	}
}
