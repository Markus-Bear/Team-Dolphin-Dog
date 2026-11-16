// The benchmark report: reads bench/results.csv and writes four graphs and summary.md beside it.
//
//	go run ./src -report bench
//
// The graphs are drawn with Go only (golang.org/x/image), so there is nothing to install.
package main

import (
	"encoding/csv"  // reading results.csv
	"fmt"           // labels and the summary
	"image"         // the picture each graph is drawn on
	"image/color"   // colours
	"image/draw"    // filling the background
	"image/png"     // saving the graphs
	"math"          // logarithmic scales and rounding
	"os"            // reading and writing the files
	"path/filepath" // file names on any system
	"slices"        // sorting
	"strconv"       // numbers from text
	"strings"       // building summary.md

	"golang.org/x/image/font"                  // measuring and drawing text
	"golang.org/x/image/font/gofont/gobold"    // the font for titles and numbers
	"golang.org/x/image/font/gofont/goregular" // the font for everything else
	"golang.org/x/image/font/opentype"         // loading the fonts
	"golang.org/x/image/math/fixed"            // text positions
	"golang.org/x/image/vector"                // filling shapes with smooth edges
)

// ---------- Reading the results ----------

// machineResults is everything measured on one machine with one set of rules.
type machineResults struct {
	name     string               // e.g. "maxred: AMD Ryzen 5 4600H (12 logical cores), 2026 rules"
	agents   []int                // the numbers of dots measured, smallest first
	cores    []int                // the numbers of cores measured, smallest first
	times    map[[2]int][]float64 // {dots, cores} → ms per day of every repeat
	deaths   map[string]int       // "dots seed from days" → deaths, to check every core count agrees
	sameDead map[int]bool         // dots → every core count gave the same deaths
}

// spread is the middle, lowest and highest of some repeats.
type spread struct{ median, lo, hi float64 }

// median returns the middle value of a list (the average of the middle two if there are two).
// One slow run moves an average but not a median.
func median(list []float64) float64 {
	s := slices.Sorted(slices.Values(list))
	if len(s) == 0 {
		return 0
	}
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// timeAt returns the ms per day at one number of dots and cores; ok is false if it wasn't measured.
func (r *machineResults) timeAt(agents, n int) (s spread, ok bool) {
	list := r.times[[2]int{agents, n}]
	if len(list) == 0 {
		return s, false
	}
	return spread{median(list), slices.Min(list), slices.Max(list)}, true
}

// speedUp returns how many times faster n cores are than one core: 1-core time ÷ n-core time.
func (r *machineResults) speedUp(agents, n int) (float64, bool) {
	one, ok1 := r.timeAt(agents, 1)
	many, ok2 := r.timeAt(agents, n)
	if !ok1 || !ok2 {
		return 0, false
	}
	return one.median / many.median, true
}

// readResults reads results.csv and sorts its rows by machine.
func readResults(name string) ([]*machineResults, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s has no results yet; run: go run ./src -bench", name)
	}
	column := map[string]int{} // where each column is, by its name in the header
	for i, title := range rows[0] {
		column[title] = i
	}
	for _, title := range benchHeader {
		if _, found := column[title]; !found {
			return nil, fmt.Errorf("%s has no %q column", name, title)
		}
	}

	var list []*machineResults
	byName := map[string]*machineResults{}
	for _, row := range rows[1:] {
		get := func(title string) string { return row[column[title]] }
		number := func(title string) int { n, _ := strconv.Atoi(get(title)); return n }
		ms, _ := strconv.ParseFloat(get("ms_per_day"), 64)

		label := fmt.Sprintf("%s: %s (%s logical cores), %s rules", get("machine"), get("cpu"), get("logical_cores"), get("era"))
		r := byName[label]
		if r == nil { // the first row from this machine
			r = &machineResults{name: label, times: map[[2]int][]float64{}, deaths: map[string]int{}, sameDead: map[int]bool{}}
			byName[label] = r
			list = append(list, r)
		}
		agents, n := number("agents"), number("cores")
		if !slices.Contains(r.agents, agents) {
			r.agents = append(r.agents, agents)
			r.sameDead[agents] = true // until two runs disagree
		}
		if !slices.Contains(r.cores, n) {
			r.cores = append(r.cores, n)
		}
		r.times[[2]int{agents, n}] = append(r.times[[2]int{agents, n}], ms)

		outbreak := strings.Join([]string{get("agents"), get("seed"), get("from_day"), get("days")}, " ")
		if dead, seen := r.deaths[outbreak]; seen && dead != number("dead") {
			r.sameDead[agents] = false // the same outbreak ended differently: something is wrong
		}
		r.deaths[outbreak] = number("dead")
	}
	for _, r := range list {
		slices.Sort(r.agents)
		slices.Sort(r.cores)
	}
	return list, nil
}

// ---------- The four graphs ----------

// series is one line on a graph.
type series struct {
	name   string    // written at the end of the line
	xs, ys []float64 // its points
	lo, hi []float64 // the lowest and highest repeat at each point (nil = no bars)
}

// chart is one graph: its text, its scales and its lines.
type chart struct {
	title, xLabel, yLabel string
	logX, logY            bool                 // logarithmic scales: each step is ×10
	xTicks                []float64            // the numbers along the bottom
	xName                 func(float64) string // how a number along the bottom is written
	value                 func(float64) string // how a value is written at the end of a line
	series                []series
	ideal                 [][2]float64 // the two ends of the dashed "ideal" line, if any
}

// dotsName writes a number of dots briefly: 10k, 200k, 1M.
func dotsName(v float64) string {
	if v >= 1e6 {
		return strconv.FormatFloat(v/1e6, 'f', -1, 64) + "M"
	}
	if v >= 1e3 {
		return strconv.FormatFloat(v/1e3, 'f', -1, 64) + "k"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// msName writes a time with about three figures: 3.42 ms, 34.2 ms, 342 ms.
func msName(v float64) string {
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f ms", v)
	case v >= 10:
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.2f ms", v)
}

func plainName(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// floats turns a list of whole numbers into the numbers along the bottom of a graph.
func floats(list []int) []float64 {
	var out []float64
	for _, v := range list {
		out = append(out, float64(v))
	}
	return out
}

// perCore builds a graph with cores along the bottom and one line for each number of dots.
// value gives the height of a point, and ok = false leaves the point out.
func (r *machineResults) perCore(c chart, value func(agents, n int) (spread, bool)) chart {
	c.xLabel, c.xTicks, c.xName = "cores", floats(r.cores), plainName
	for _, agents := range r.agents {
		s := series{name: dotsName(float64(agents)) + " dots"}
		for _, n := range r.cores {
			if v, ok := value(agents, n); ok {
				s.xs, s.ys = append(s.xs, float64(n)), append(s.ys, v.median)
				s.lo, s.hi = append(s.lo, v.lo), append(s.hi, v.hi)
			}
		}
		c.series = append(c.series, s)
	}
	return c
}

// speedUpChart: how many times faster than one core. The dashed line is the ideal: N cores, N times faster.
func (r *machineResults) speedUpChart() chart {
	most := float64(r.cores[len(r.cores)-1])
	c := chart{title: "Speed-up", yLabel: "times faster than 1 core", ideal: [][2]float64{{1, 1}, {most, most}},
		value: func(v float64) string { return fmt.Sprintf("%.1f×", v) }}
	return r.perCore(c, func(agents, n int) (spread, bool) {
		v, ok := r.speedUp(agents, n)
		return spread{v, v, v}, ok
	})
}

// timeChart: the measured times themselves, with a bar from the fastest repeat to the slowest.
func (r *machineResults) timeChart() chart {
	c := chart{title: "Time for one day", yLabel: "ms per simulated day (each step is ×10)", logY: true, value: msName}
	return r.perCore(c, r.timeAt)
}

// efficiencyChart: the speed-up as a share of the ideal. 100% means no core's time is wasted.
func (r *machineResults) efficiencyChart() chart {
	most := float64(r.cores[len(r.cores)-1])
	c := chart{title: "Efficiency", yLabel: "speed-up ÷ cores, %", ideal: [][2]float64{{1, 100}, {most, 100}},
		value: func(v float64) string { return fmt.Sprintf("%.0f%%", v) }}
	return r.perCore(c, func(agents, n int) (spread, bool) {
		v, ok := r.speedUp(agents, n)
		v = 100 * v / float64(n)
		return spread{v, v, v}, ok
	})
}

// scalingChart: dots along the bottom and one line for each number of cores, as on the whiteboard.
func (r *machineResults) scalingChart() chart {
	c := chart{title: "Time for one day as the dots grow", xLabel: "dots (each step is ×10)", yLabel: "ms per simulated day (each step is ×10)",
		logX: true, logY: true, xTicks: floats(r.agents), xName: dotsName, value: msName}
	for _, n := range r.cores {
		s := series{name: fmt.Sprintf("%d cores", n)}
		if n == 1 {
			s.name = "1 core"
		}
		for _, agents := range r.agents {
			if v, ok := r.timeAt(agents, n); ok {
				s.xs, s.ys = append(s.xs, float64(agents)), append(s.ys, v.median)
				s.lo, s.hi = append(s.lo, v.lo), append(s.hi, v.hi)
			}
		}
		c.series = append(c.series, s)
	}
	return c
}

// ---------- Drawing ----------

// Sizes, in points. Everything is drawn at twice this size so the graphs stay sharp when enlarged.
const (
	drawScale   = 2
	panelWidth  = 900 // one machine's graph
	panelHeight = 600
	panelGap    = 20 // the small space between two graphs
	chartLeft   = 80 // from the panel's edge to the box
	chartRight  = 210
	chartTop    = 110
	chartBottom = 70
	labelGap    = 26 // the smallest distance between two labels at the right of a box
)

var (
	inkColour   = color.RGBA{0x1a, 0x1a, 0x1a, 0xff} // text and the box
	softColour  = color.RGBA{0x66, 0x66, 0x66, 0xff} // scale numbers and the ideal line
	gridColour  = color.RGBA{0xe2, 0xe2, 0xe2, 0xff} // faint lines across the box
	paperColour = color.RGBA{0xff, 0xff, 0xff, 0xff} // the background

	// The Okabe–Ito colours, which stay apart for colour-blind readers. Each line also has its own
	// marker shape, so the graphs still work in black and white.
	lineColours = []color.RGBA{
		{0x00, 0x72, 0xb2, 0xff}, // blue
		{0xd5, 0x5e, 0x00, 0xff}, // vermilion
		{0x00, 0x9e, 0x73, 0xff}, // green
		{0xcc, 0x79, 0xa7, 0xff}, // purple
		{0xe6, 0x9f, 0x00, 0xff}, // orange
		{0x56, 0xb4, 0xe9, 0xff}, // sky blue
		{0x1a, 0x1a, 0x1a, 0xff}, // black
	}
)

// canvas is a picture with the fonts to write on it.
type canvas struct {
	img                *image.RGBA
	big, bold, regular font.Face
}

// newCanvas makes a white picture of the given size (in points).
func newCanvas(width, height int) (*canvas, error) {
	c := &canvas{img: image.NewRGBA(image.Rect(0, 0, width*drawScale, height*drawScale))}
	draw.Draw(c.img, c.img.Bounds(), image.NewUniform(paperColour), image.Point{}, draw.Src)
	face := func(ttf []byte, size float64) (font.Face, error) {
		f, err := opentype.Parse(ttf)
		if err != nil {
			return nil, err
		}
		return opentype.NewFace(f, &opentype.FaceOptions{Size: size * drawScale, DPI: 72})
	}
	var err error
	if c.big, err = face(gobold.TTF, 28); err != nil {
		return nil, err
	}
	if c.bold, err = face(gobold.TTF, 17); err != nil {
		return nil, err
	}
	if c.regular, err = face(goregular.TTF, 16); err != nil {
		return nil, err
	}
	return c, nil
}

// fill colours in a shape given by its corners, with smooth edges.
func (c *canvas) fill(colour color.RGBA, corners ...[2]float64) {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1) // the box the shape fits in
	for _, p := range corners {
		x0, y0 = math.Min(x0, p[0]*drawScale), math.Min(y0, p[1]*drawScale)
		x1, y1 = math.Max(x1, p[0]*drawScale), math.Max(y1, p[1]*drawScale)
	}
	left, top := int(math.Floor(x0)), int(math.Floor(y0))
	ras := vector.NewRasterizer(int(math.Ceil(x1))-left+1, int(math.Ceil(y1))-top+1)
	for i, p := range corners {
		x, y := float32(p[0]*drawScale)-float32(left), float32(p[1]*drawScale)-float32(top)
		if i == 0 {
			ras.MoveTo(x, y)
		} else {
			ras.LineTo(x, y)
		}
	}
	ras.ClosePath()
	ras.Draw(c.img, ras.Bounds().Add(image.Pt(left, top)), image.NewUniform(colour), image.Point{})
}

// line draws a straight line of the given thickness: a long thin rectangle.
func (c *canvas) line(x0, y0, x1, y1, thick float64, colour color.RGBA) {
	length := math.Hypot(x1-x0, y1-y0)
	if length == 0 {
		return
	}
	nx, ny := -(y1-y0)/length*thick/2, (x1-x0)/length*thick/2 // half the thickness, sideways
	c.fill(colour, [2]float64{x0 + nx, y0 + ny}, [2]float64{x1 + nx, y1 + ny}, [2]float64{x1 - nx, y1 - ny}, [2]float64{x0 - nx, y0 - ny})
}

// dashed draws a line as dashes with gaps between them.
func (c *canvas) dashed(x0, y0, x1, y1, thick float64, colour color.RGBA) {
	const dash, gap = 12.0, 8.0
	length := math.Hypot(x1-x0, y1-y0)
	for at := 0.0; at < length; at += dash + gap {
		a, b := at/length, math.Min(at+dash, length)/length // where this dash starts and ends, 0 to 1
		c.line(x0+(x1-x0)*a, y0+(y1-y0)*a, x0+(x1-x0)*b, y0+(y1-y0)*b, thick, colour)
	}
}

// marker draws the shape that marks a measured point: a circle, square, triangle, diamond, cross
// or upside-down triangle, by the line's number. A cross is two short lines; the rest are filled.
func (c *canvas) marker(shape int, x, y, r float64, colour color.RGBA) {
	var corners [][2]float64
	polygon := func(sides int, turn, size float64) { // a regular shape with this many corners
		for i := 0; i < sides; i++ {
			angle := turn + 2*math.Pi*float64(i)/float64(sides)
			corners = append(corners, [2]float64{x + size*math.Sin(angle), y - size*math.Cos(angle)})
		}
	}
	switch shape % 6 {
	case 0:
		polygon(24, 0, r) // near enough a circle
	case 1:
		polygon(4, math.Pi/4, r*1.25) // square
	case 2:
		polygon(3, 0, r*1.4) // triangle
	case 3:
		polygon(4, 0, r*1.35) // diamond
	case 4:
		c.line(x-r, y-r, x+r, y+r, r*0.7, colour) // cross
		c.line(x-r, y+r, x+r, y-r, r*0.7, colour)
		return
	case 5:
		polygon(3, math.Pi, r*1.4) // upside-down triangle
	}
	c.fill(colour, corners...)
}

// text writes s with its baseline at y. align 0 starts it at x, 0.5 centres it on x, 1 ends it at x.
func (c *canvas) text(face font.Face, s string, x, y, align float64, colour color.RGBA) {
	width := font.MeasureString(face, s)
	d := font.Drawer{Dst: c.img, Src: image.NewUniform(colour), Face: face}
	d.Dot = fixed.Point26_6{X: fixed.I(int(x*drawScale)) - fixed.Int26_6(float64(width)*align), Y: fixed.I(int(y * drawScale))}
	d.DrawString(s)
}

// niceStep rounds v up to 1, 2 or 5 times a power of ten, for scale numbers that are easy to read.
func niceStep(v float64) float64 {
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, f := range []float64{1, 2, 5} {
		if f*p >= v {
			return f * p
		}
	}
	return 10 * p
}

// drawChart draws one graph with its top-left corner at (left, top): the title, the machine's
// name, the box with its scales, the lines, and each line's name and last value at its right end.
func (c *canvas) drawChart(ch chart, machine string, left, top float64) {
	x0, x1 := left+chartLeft, left+panelWidth-chartRight // the box
	y0, y1 := top+chartTop, top+panelHeight-chartBottom

	// The range of each scale, from the values themselves.
	xMin, xMax := 0.0, slices.Max(ch.xTicks)
	if ch.logX {
		xMin, xMax = slices.Min(ch.xTicks)/1.5, xMax*1.5 // a little room either side
	}
	low, high := math.Inf(1), 0.0
	for _, s := range ch.series {
		for i := range s.ys {
			low, high = math.Min(low, s.lo[i]), math.Max(high, s.hi[i])
		}
	}
	var yMin, yMax float64
	var yTicks []float64
	if ch.logY { // whole powers of ten around the values, with 2× and 5× in between
		yMin, yMax = math.Pow(10, math.Floor(math.Log10(low))), math.Pow(10, math.Ceil(math.Log10(high)))
		for p := yMin; p < yMax*0.99; p *= 10 {
			yTicks = append(yTicks, p, 2*p, 5*p)
		}
		yTicks = append(yTicks, yMax)
	} else { // from 0 to a round number above the highest value
		step := niceStep(high * 1.1 / 5)
		yMax = math.Ceil(high*1.1/step) * step
		for v := 0.0; v <= yMax+step/2; v += step {
			yTicks = append(yTicks, v)
		}
	}
	place := func(v, lo, hi float64, log bool) float64 { // where a value is along a scale, 0 to 1
		if log {
			return (math.Log(v) - math.Log(lo)) / (math.Log(hi) - math.Log(lo))
		}
		return (v - lo) / (hi - lo)
	}
	px := func(v float64) float64 { return x0 + (x1-x0)*place(v, xMin, xMax, ch.logX) }
	py := func(v float64) float64 { return y1 - (y1-y0)*place(v, yMin, yMax, ch.logY) }

	// Title, machine, and what the upright scale measures.
	c.text(c.big, ch.title, left+12, top+38, 0, inkColour)
	c.text(c.regular, machine, left+12, top+64, 0, softColour)
	c.text(c.bold, ch.yLabel, left+12, y0-14, 0, inkColour)

	// Faint lines across with their numbers, the numbers along the bottom, then the box.
	for _, v := range yTicks {
		c.line(x0, py(v), x1, py(v), 1, gridColour)
		c.text(c.regular, plainName(math.Round(v*1000)/1000), x0-10, py(v)+6, 1, softColour)
	}
	for _, v := range ch.xTicks {
		c.line(px(v), y0, px(v), y1, 1, gridColour)
		c.text(c.regular, ch.xName(v), px(v), y1+24, 0.5, softColour)
	}
	c.text(c.bold, ch.xLabel, (x0+x1)/2, y1+52, 0.5, inkColour)
	for _, e := range [][4]float64{{x0, y0, x1, y0}, {x0, y1, x1, y1}, {x0, y0, x0, y1}, {x1, y0, x1, y1}} {
		c.line(e[0], e[1], e[2], e[3], 1.5, softColour)
	}

	// The dashed ideal line, cut off where it would leave the top of the box.
	if ch.ideal != nil {
		a, b := ch.ideal[0], ch.ideal[1]
		if b[1] > yMax { // it rises out of the box: stop at the top
			share := (yMax - a[1]) / (b[1] - a[1])
			b = [2]float64{a[0] + (b[0]-a[0])*share, yMax}
		}
		c.dashed(px(a[0]), py(a[1]), px(b[0]), py(b[1]), 2.5, softColour)
		c.text(c.regular, "ideal", px(b[0])-22, py(b[1])+24, 1, softColour)
	}

	// The lines: bars from the fastest to the slowest repeat, a thick line, then a marker on every
	// measured point (with a white edge so markers stand out where lines cross).
	type label struct {
		text   string
		colour color.RGBA
		y      float64
	}
	var labels []label
	for i, s := range ch.series {
		if len(s.xs) == 0 {
			continue
		}
		colour := lineColours[i%len(lineColours)]
		for j := range s.xs {
			if s.hi[j] > s.lo[j] {
				x := px(s.xs[j])
				c.line(x, py(s.lo[j]), x, py(s.hi[j]), 2, colour)
				c.line(x-6, py(s.lo[j]), x+6, py(s.lo[j]), 2, colour) // the caps
				c.line(x-6, py(s.hi[j]), x+6, py(s.hi[j]), 2, colour)
			}
		}
		for j := 1; j < len(s.xs); j++ {
			c.line(px(s.xs[j-1]), py(s.ys[j-1]), px(s.xs[j]), py(s.ys[j]), 4, colour)
		}
		for j := range s.xs {
			c.marker(i, px(s.xs[j]), py(s.ys[j]), 10, paperColour)
			c.marker(i, px(s.xs[j]), py(s.ys[j]), 7.5, colour)
		}
		last := len(s.xs) - 1
		labels = append(labels, label{s.name + "  " + ch.value(s.ys[last]), colour, py(s.ys[last]) + 6})
	}

	// Each line's name and last value, level with its right end. Labels that would overlap are
	// moved apart: pushed down, then up again if that went past the bottom (as in graphs.go).
	slices.SortFunc(labels, func(a, b label) int { return int(math.Round(a.y - b.y)) })
	for i := range labels {
		if i > 0 {
			labels[i].y = math.Max(labels[i].y, labels[i-1].y+labelGap)
		}
	}
	for i := len(labels) - 1; i >= 0; i-- {
		limit := y1 + 6
		if i < len(labels)-1 {
			limit = labels[i+1].y - labelGap
		}
		labels[i].y = math.Min(labels[i].y, limit)
	}
	for _, lb := range labels {
		c.text(c.bold, lb.text, x1+16, lb.y, 0, lb.colour)
	}
}

// writeGraph draws one graph per machine, one under the other with a small space between, and
// saves the picture.
func writeGraph(name string, machines []*machineResults, build func(*machineResults) chart) error {
	c, err := newCanvas(panelWidth, len(machines)*(panelHeight+panelGap)-panelGap)
	if err != nil {
		return err
	}
	for i, r := range machines {
		top := float64(i * (panelHeight + panelGap))
		if i > 0 { // a thin line in the space between two graphs
			c.line(12, top-panelGap/2, panelWidth-12, top-panelGap/2, 1, gridColour)
		}
		c.drawChart(build(r), r.name, 0, top)
	}
	file, err := os.Create(name)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, c.img)
}

// ---------- The report ----------

// writeReport reads results.csv in dir and writes the four graphs and summary.md beside it.
func writeReport(dir string) error {
	machines, err := readResults(filepath.Join(dir, benchFile))
	if err != nil {
		return err
	}
	graphs := []struct {
		file, title string
		build       func(*machineResults) chart
	}{
		{"speedup.png", "Speed-up", (*machineResults).speedUpChart},
		{"time.png", "Time for one day", (*machineResults).timeChart},
		{"efficiency.png", "Efficiency", (*machineResults).efficiencyChart},
		{"scaling.png", "Time as the dots grow", (*machineResults).scalingChart},
	}
	var b strings.Builder
	b.WriteString("# Benchmark results\n\nMade by `go run ./src -report " + dir + "` from `" + benchFile + "`. Don't edit it by hand.\n\n")
	for _, g := range graphs {
		if err := writeGraph(filepath.Join(dir, g.file), machines, g.build); err != nil {
			return err
		}
		fmt.Fprintf(&b, "![%s](%s)\n\n", g.title, g.file)
	}

	// One table for each machine and number of dots.
	for _, r := range machines {
		fmt.Fprintf(&b, "## %s\n\n", r.name)
		for _, agents := range r.agents {
			same := "✓ same deaths on every core count"
			if !r.sameDead[agents] {
				same = "✗ DIFFERENT deaths on different core counts"
			}
			fmt.Fprintf(&b, "**%s dots** — %s\n\n", thousands(float64(agents)), same)
			b.WriteString("| Cores | Median ms/day | Fastest – slowest | Runs | Speed-up | Efficiency |\n|---|---|---|---|---|---|\n")
			for _, n := range r.cores {
				t, ok := r.timeAt(agents, n)
				if !ok {
					continue
				}
				speed, efficiency := "–", "–" // unknown without a 1-core time
				if s, ok := r.speedUp(agents, n); ok {
					speed, efficiency = fmt.Sprintf("%.2f×", s), fmt.Sprintf("%.0f%%", 100*s/float64(n))
				}
				fmt.Fprintf(&b, "| %d | %.2f | %.2f – %.2f | %d | %s | %s |\n", n, t.median, t.lo, t.hi, len(r.times[[2]int{agents, n}]), speed, efficiency)
			}
			b.WriteString("\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote speedup.png, time.png, efficiency.png, scaling.png and summary.md to %s\n", dir)
	return nil
}
