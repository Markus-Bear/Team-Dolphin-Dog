// The window: drawing, zooming, panning and keyboard controls.
package main

import (
	"bytes"       // reading the built-in font
	"fmt"         // building text for the screen
	"image/color" // colours
	"log"         // stopping with an error message
	"math"        // zoom and scale-bar sums

	"github.com/hajimehoshi/ebiten/v2"           // the window and drawing
	"github.com/hajimehoshi/ebiten/v2/inpututil" // single key presses
	"github.com/hajimehoshi/ebiten/v2/text/v2"   // drawing text
	"github.com/hajimehoshi/ebiten/v2/vector"    // drawing rectangles and circles
	"golang.org/x/image/font/gofont/goregular"   // the font
)

// ---------- Colours ----------

// Bright enough to show up on the satellite view.
var (
	Green     = color.RGBA{0x1a, 0x98, 0x50, 0xff} // healthy
	Orange    = color.RGBA{0xf4, 0xa5, 0x24, 0xff} // caught bubonic, not ill yet
	Yellow    = color.RGBA{0xf0, 0xe0, 0x30, 0xff} // caught pneumonic, not ill yet
	Red       = color.RGBA{0xe0, 0x10, 0x30, 0xff} // ill with bubonic
	Purple    = color.RGBA{0xa0, 0x50, 0xe0, 0xff} // ill with pneumonic
	Blue      = color.RGBA{0x45, 0x95, 0xf4, 0xff} // recovered
	Grey      = color.RGBA{0x55, 0x55, 0x55, 0xff} // the dead (in the legend and graphs; they aren't drawn), and small print
	Brown     = color.RGBA{0x8c, 0x5a, 0x28, 0xff} // plague in the rats and fleas
	OffMap    = color.RGBA{0x20, 0x20, 0x20, 0xff} // around the edges of the map
	StartMark = color.RGBA{0xff, 0xd7, 0x00, 0xff} // the ring where the outbreak began
	Border    = color.RGBA{0xff, 0xff, 0xff, 0xa0} // county borders on the satellite: see-through white
	DarkLine  = color.RGBA{0x30, 0x30, 0x30, 0xd0} // county borders on the paler maps: see-through grey
	SealedRed = color.RGBA{0xd0, 0x10, 0x10, 0xff} // sealed counties' tint and name labels
)

// stateColours are the dot colours for each health state.
var stateColours = [numStates]color.RGBA{Green, Orange, Yellow, Red, Purple, Blue, Grey}

// areaStops colour the area view by the share of an area's people. Shares in between are
// blended on a log scale, so 0.5% → 2% looks as big a step as 10% → 50%.
var areaStops = []struct {
	share float64
	c     color.RGBA
}{
	{0.005, color.RGBA{0xff, 0xf3, 0xa0, 0xff}}, // 0.5%: pale yellow
	{0.02, color.RGBA{0xfd, 0x8d, 0x3c, 0xff}},  // 2%: orange
	{0.1, color.RGBA{0xe3, 0x1a, 0x1c, 0xff}},   // 10%: red
	{0.5, color.RGBA{0x67, 0x00, 0x0d, 0xff}},   // 50% or more: dark red
}

// populationKey matches the colours of maps/ireland_population.png (people per km²).
var populationKey = []LegendItem{
	{"no people", color.RGBA{0xe6, 0xe3, 0xdc, 0xff}},
	{"1", color.RGBA{0xff, 0xff, 0xcc, 0xff}},
	{"10", color.RGBA{0xfe, 0xd9, 0x76, 0xff}},
	{"100", color.RGBA{0xfd, 0x8d, 0x3c, 0xff}},
	{"1,000", color.RGBA{0xe3, 0x1a, 0x1c, 0xff}},
	{"10,000 or more", color.RGBA{0x80, 0x00, 0x26, 0xff}},
}

// ---------- Layout ----------

// LegendItem is one coloured marker with a label.
type LegendItem struct {
	Label  string
	Colour color.RGBA
}

// Sizes and positions, in screen pixels unless marked.
const (
	windowWidth   = 1440 // the map, then the graphs (graphs.go) on the right
	windowHeight  = 922
	plotLeft      = 20  // the map area's left edge
	plotTop       = 70  // the map area's top edge, below the legend
	plotWidth     = 960 // the map area's size
	plotHeight    = 740
	plotMidX      = plotLeft + plotWidth/2 // the middle of the map area
	plotMidY      = plotTop + plotHeight/2
	markerRadius  = 4   // legend dots
	legendColumns = 5   // legend entries per row
	legendWidth   = 196 // width of each legend entry
	maxZoom       = 16  // closest zoom: screen pixels per km
	panSpeed      = 10  // screen pixels moved per frame with the arrow keys
	startSpeed    = 3.5 // simulated days per second when the window opens

	areaCell    = 10   // area-view squares, km
	areaMinDots = 5    // squares with fewer dots are left blank; a share of 1–4 dots is noise
	areaAlpha   = 0.75 // see-through, so the map shows underneath
)

// ---------- Views ----------

// view is what the map shows; V cycles through them.
type view int

const (
	dotsView     view = iota // one dot per agent
	infectedView             // share of each square infected now
	deadView                 // share of each square dead so far
	numViews                 // how many views there are
)

var viewNames = [numViews]string{"dots", "% infected now", "% dead"} // shown in the keys line and the key

// ---------- The game ----------

// Game holds everything the window needs between frames.
type Game struct {
	world     *World           // the outbreak being shown
	m         *Map             // the island
	era       int              // which of eras is in use
	paused    bool             // Space stops the days going by
	bg        int              // which background is shown
	view      view             // dots or one of the area views
	bgImages  []*ebiten.Image  // made the first time each background is shown
	seed      uint64           // the current outbreak's seed
	font      *text.GoTextFace // for all text
	smallFont *text.GoTextFace // for county names
	stepTimer float64          // fraction of a day waiting to be simulated
	speed     float64          // simulated days per second; [ and ] halve or double it

	centerX, centerY float64 // map position in the middle of the view
	zoom             float64 // screen pixels per km

	// Layers over the background. They are repainted only when something changes; Ebitengine
	// scales and combines them every frame.
	canvas  *ebiten.Image // the map area, put together each frame
	dots    *layer        // one pixel per screen pixel
	shade   *layer        // rat shading, one pixel per km
	area    *layer        // area view, one pixel per area square
	tint    *layer        // red over sealed counties, one pixel per km
	borders *layer        // county borders, one pixel per screen pixel
	drawn   string        // what the layers show, to skip repainting when nothing changed

	mouseX, mouseY int  // pointer position last frame, for dragging
	dragging       bool // the mouse button was already down last frame
	pressX, pressY int  // where the mouse button went down, to tell a click from a drag
}

// layer is a picture over the background: pixels painted on the CPU, then sent to the graphics card.
type layer struct {
	img  *ebiten.Image
	pix  []byte // four bytes per pixel, premultiplied by alpha
	w, h int    // size in pixels
}

func newLayer(w, h int) *layer {
	return &layer{img: ebiten.NewImage(w, h), pix: make([]byte, w*h*4), w: w, h: h}
}

// send copies the painted pixels to the graphics card.
func (l *layer) send() { l.img.WritePixels(l.pix) }

// fitZoom is the zoom that shows the whole island.
func (g *Game) fitZoom() float64 {
	return math.Min(plotWidth/float64(g.m.W), plotHeight/float64(g.m.H)) // whichever of width or height fits first
}

// resetView shows the whole island (the 0 key).
func (g *Game) resetView() {
	g.zoom = g.fitZoom()
	g.centerX, g.centerY = float64(g.m.W)/2, float64(g.m.H)/2 // the middle of the map
}

// restart begins a new outbreak with the current era and seed (R and Y).
func (g *Game) restart() {
	g.world = newWorld(g.m, eras[g.era], g.seed)
	g.stepTimer = 0
}

// origin is the map position at the top-left corner of the view.
func (g *Game) origin() (float64, float64) {
	return g.centerX - plotWidth/2/g.zoom, g.centerY - plotHeight/2/g.zoom // half a view left and up from the middle
}

// toScreen turns a map position (km) into a position in the map area (screen pixels).
func (g *Game) toScreen(x, y float64) (float32, float32) {
	left, top := g.origin()
	return float32((x - left) * g.zoom), float32((y - top) * g.zoom)
}

// ---------- Input and simulation ----------

// Update runs 60 times a second: input first, then as many simulated days as are due.
func (g *Game) Update() error {
	g.handleKeys()
	g.handlePan()
	g.handleMouse()
	g.handleZoom()
	g.clampView() // don't let the view leave the map
	g.runDueDays()
	return nil
}

// handleKeys deals with the keys that act once per press.
func (g *Game) handleKeys() {
	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeySpace):
		g.paused = !g.paused // pause or carry on
	case inpututil.IsKeyJustPressed(ebiten.KeyR):
		g.seed = randomSeed() // a new outbreak
		g.restart()
	case inpututil.IsKeyJustPressed(ebiten.KeyY):
		g.era = (g.era + 1) % len(eras) // the other era, same seed, so the two can be compared
		g.restart()
	case inpututil.IsKeyJustPressed(ebiten.KeyB):
		g.bg = (g.bg + 1) % len(g.m.Backgrounds) // the next background
	case inpututil.IsKeyJustPressed(ebiten.KeyV):
		g.view = (g.view + 1) % numViews // the next view
	case inpututil.IsKeyJustPressed(ebiten.Key0), inpututil.IsKeyJustPressed(ebiten.KeyNumpad0):
		g.resetView() // the whole island
	case inpututil.IsKeyJustPressed(ebiten.KeyBracketRight):
		g.speed = math.Min(g.speed*2, 112) // twice as fast, up to 112 days a second
	case inpututil.IsKeyJustPressed(ebiten.KeyBracketLeft):
		g.speed = math.Max(g.speed/2, 0.875) // half as fast
	}
}

// panKeys are the arrow keys and the direction each moves the view.
var panKeys = []struct {
	key    ebiten.Key
	dx, dy float64
}{{ebiten.KeyArrowLeft, -1, 0}, {ebiten.KeyArrowRight, 1, 0}, {ebiten.KeyArrowUp, 0, -1}, {ebiten.KeyArrowDown, 0, 1}}

// handlePan moves the view while an arrow key is held.
func (g *Game) handlePan() {
	step := panSpeed / g.zoom // the same screen distance at any zoom
	for _, p := range panKeys {
		if ebiten.IsKeyPressed(p.key) {
			g.centerX += p.dx * step
			g.centerY += p.dy * step
		}
	}
}

// handleMouse drags the map, and treats a press and release without moving as a click.
func (g *Game) handleMouse() {
	mx, my := ebiten.CursorPosition()
	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		if g.dragging { // the map follows the pointer since last frame
			g.centerX -= float64(mx-g.mouseX) / g.zoom
			g.centerY -= float64(my-g.mouseY) / g.zoom
		} else {
			g.pressX, g.pressY = mx, my // just pressed
		}
		g.dragging = true
	} else {
		if g.dragging && abs(mx-g.pressX)+abs(my-g.pressY) < 4 { // let go without moving: a click
			g.click(mx, my)
		}
		g.dragging = false
	}
	g.mouseX, g.mouseY = mx, my // remembered for next frame
}

// handleZoom zooms on the middle of the map with +/−, and on the pointer with the wheel.
func (g *Game) handleZoom() {
	if ebiten.IsKeyPressed(ebiten.KeyEqual) || ebiten.IsKeyPressed(ebiten.KeyNumpadAdd) {
		g.zoomBy(1.04, plotMidX, plotMidY) // 4% closer each frame the key is held
	}
	if ebiten.IsKeyPressed(ebiten.KeyMinus) || ebiten.IsKeyPressed(ebiten.KeyNumpadSubtract) {
		g.zoomBy(1/1.04, plotMidX, plotMidY)
	}
	if _, wheel := ebiten.Wheel(); wheel != 0 {
		mx, my := ebiten.CursorPosition()
		g.zoomBy(math.Pow(1.15, wheel), float64(mx), float64(my)) // 15% per wheel step
	}
}

// runDueDays simulates each whole day that is due, unless paused or the outbreak is over.
func (g *Game) runDueDays() {
	if g.paused || g.world.Over() {
		return
	}
	g.stepTimer += g.speed / 60                                      // this frame's share of a day
	for n := 0; g.stepTimer >= 1 && n < 20 && !g.world.Over(); n++ { // at most 20 days a frame
		g.world.Step()
		g.stepTimer--
	}
}

// click seals or opens the county under screen position (sx, sy).
func (g *Game) click(sx, sy int) {
	if !onPlot(float64(sx), float64(sy), 1, 1) { // not on the map
		return
	}
	left, top := g.origin()
	x, y := left+float64(sx-plotLeft)/g.zoom, top+float64(sy-plotTop)/g.zoom // screen to map (km)
	if c := g.m.CountyAt(x, y); c > 0 {
		g.world.toggleSeal(c)
		g.drawn = "" // repaint the red tint
	}
}

// onPlot reports whether a box on the screen is fully inside the map area.
func onPlot(x, y, w, h float64) bool {
	return x >= plotLeft && y >= plotTop && x+w <= plotLeft+plotWidth && y+h <= plotTop+plotHeight
}

// abs is the size of n, without its sign.
func abs(n int) int {
	return max(n, -n)
}

// zoomBy magnifies by factor, keeping the map point under screen position (sx, sy) in place.
func (g *Game) zoomBy(factor, sx, sy float64) {
	before := g.zoom
	g.zoom = math.Max(g.fitZoom(), math.Min(g.zoom*factor, maxZoom)) // no further out than the whole island, no closer than maxZoom
	ox, oy := sx-plotMidX, sy-plotMidY                               // the point's distance from the middle of the view
	g.centerX += ox/before - ox/g.zoom                               // shift so that point stays under the pointer
	g.centerY += oy/before - oy/g.zoom
}

// clampView keeps the view on the map.
func (g *Game) clampView() {
	halfW, halfH := plotWidth/2/g.zoom, plotHeight/2/g.zoom // half the view, in km
	clamp := func(c, half, size float64) float64 {
		if 2*half >= size { // the whole map fits this way
			return size / 2 // so centre it
		}
		return math.Max(half, math.Min(c, size-half)) // otherwise stop at the edges
	}
	g.centerX = clamp(g.centerX, halfW, float64(g.m.W))
	g.centerY = clamp(g.centerY, halfH, float64(g.m.H))
}

// ---------- Painting the layers ----------

// setPixel writes a colour into an RGBA buffer. Ebitengine wants colours premultiplied by alpha.
func setPixel(pix []byte, i int, c color.RGBA, alpha float64) {
	pix[i*4] = byte(float64(c.R) * alpha)   // red
	pix[i*4+1] = byte(float64(c.G) * alpha) // green
	pix[i*4+2] = byte(float64(c.B) * alpha) // blue
	pix[i*4+3] = byte(255 * alpha)          // how see-through
}

// paintShade colours the land brown where rats and fleas carry plague, more strongly where
// there is more of it.
func (g *Game) paintShade() {
	w, m := g.world, g.m
	for i, land := range m.land { // every km of the map
		shade := 0.0 // sea stays clear
		if land {
			cx, cy := w.cellOf(float64(i%m.W), float64(i/m.W)) // the rat area this km is in
			c := w.Cells[cy*w.GridW+cx]
			shade = min(1, (c.Infected+c.Fleas)*3) * 0.55 // stronger with more plague, at most 55%
		}
		setPixel(g.shade.pix, i, Brown, shade)
	}
	g.shade.send()
}

// paintDots draws one dot per living agent. Dots grow as you zoom in; the dead aren't drawn.
func (g *Game) paintDots() {
	clear(g.dots.pix) // start see-through
	r := 0            // dot radius: 0 is a single pixel
	switch {
	case g.zoom >= 10:
		r = 2
	case g.zoom >= 4:
		r = 1
	}
	for _, a := range g.world.Agents {
		if a.State == Dead {
			continue
		}
		sx, sy := g.toScreen(a.X, a.Y)
		for dy := -r; dy <= r; dy++ { // the pixels in a square around the dot
			for dx := -r; dx <= r; dx++ {
				x, y := int(sx)+dx, int(sy)+dy
				if dx*dx+dy*dy <= r*r+r && x >= 0 && y >= 0 && x < plotWidth && y < plotHeight { // inside the circle and on screen
					setPixel(g.dots.pix, y*plotWidth+x, stateColours[a.State], 1)
				}
			}
		}
	}
	g.dots.send()
}

// paintArea colours each 10 km square by the share of its dots that are infected now (caught
// it or ill) or dead. Dots are counted where they are; the dead stay where they died.
func (g *Game) paintArea() {
	cols, rows := g.area.w, g.area.h
	total := make([]int, cols*rows) // dots in each square
	hit := make([]int, cols*rows)   // of those, infected now or dead
	for _, a := range g.world.Agents {
		i := int(a.Y)/areaCell*cols + int(a.X)/areaCell // the square they're in
		total[i]++
		infected := a.State >= ExposedBubonic && a.State <= Pneumonic // caught it or ill
		if (g.view == infectedView && infected) || (g.view == deadView && a.State == Dead) {
			hit[i]++
		}
	}
	clear(g.area.pix) // start see-through
	for i := range total {
		if total[i] >= areaMinDots && hit[i] > 0 { // enough dots to trust, and something to show
			setPixel(g.area.pix, i, areaColour(float64(hit[i])/float64(total[i])), areaAlpha)
		}
	}
	g.area.send()
}

// areaColour picks the colour for a share between the areaStops.
func areaColour(share float64) color.RGBA {
	if share <= areaStops[0].share { // below the first stop
		return areaStops[0].c
	}
	for i := 1; i < len(areaStops); i++ {
		lo, hi := areaStops[i-1], areaStops[i] // the two stops either side
		if share <= hi.share {
			t := math.Log(share/lo.share) / math.Log(hi.share/lo.share)                            // how far from lo to hi, on a log scale
			mix := func(a, b uint8) uint8 { return uint8(float64(a) + t*(float64(b)-float64(a))) } // blend one colour channel
			return color.RGBA{mix(lo.c.R, hi.c.R), mix(lo.c.G, hi.c.G), mix(lo.c.B, hi.c.B), 0xff}
		}
	}
	return areaStops[len(areaStops)-1].c // above the last stop
}

// paintTint colours sealed counties a faint red.
func (g *Game) paintTint() {
	for i, c := range g.m.county { // every km of the map
		alpha := 0.0
		if g.world.Sealed[c] && c != 0 { // land in a sealed county
			alpha = 0.22
		}
		setPixel(g.tint.pix, i, SealedRed, alpha)
	}
	g.tint.send()
}

// paintBorders draws a one-pixel line wherever neighbouring screen pixels are in different
// counties, so the lines stay thin at any zoom.
func (g *Game) paintBorders() {
	line := Border
	if g.m.Backgrounds[g.bg].Name != "satellite" {
		line = DarkLine // white wouldn't show on the paler maps
	}
	left, top := g.origin()
	ids := make([]int, plotWidth*plotHeight) // the county under each screen pixel
	for sy := 0; sy < plotHeight; sy++ {
		for sx := 0; sx < plotWidth; sx++ {
			ids[sy*plotWidth+sx] = g.m.fineCountyAt(left+(float64(sx)+0.5)/g.zoom, top+(float64(sy)+0.5)/g.zoom)
		}
	}
	clear(g.borders.pix) // start see-through
	for i, c := range ids {
		if c == 0 { // sea: the coast is already on the background
			continue
		}
		right, below := 0, 0 // the counties to the right and below
		if i%plotWidth < plotWidth-1 {
			right = ids[i+1]
		}
		if i+plotWidth < len(ids) {
			below = ids[i+plotWidth]
		}
		if (right != 0 && right != c) || (below != 0 && below != c) { // a different county next door
			setPixel(g.borders.pix, i, color.RGBA{line.R, line.G, line.B, 0xff}, float64(line.A)/255)
		}
	}
	g.borders.send()
}

// drawCountyNames writes each county's name at its label point; sealed counties' names sit on a
// red label.
func (g *Game) drawCountyNames(screen *ebiten.Image) {
	for c, county := range g.m.Counties {
		if c == 0 { // the sea has no name
			continue
		}
		x, y := g.toScreen(county.LabelX, county.LabelY)
		w, h := text.Measure(county.Name, g.smallFont, 0)
		sx, sy := float64(x)+plotLeft-w/2, float64(y)+plotTop-h/2 // centred on the label point
		if !onPlot(sx, sy, w, h) {                                // off the map area
			continue
		}
		if g.world.Sealed[c] {
			vector.FillRect(screen, float32(sx-3), float32(sy-1), float32(w+6), float32(h+2), SealedRed, false) // red label behind the name
		} else {
			g.drawTextWith(screen, g.smallFont, county.Name, sx+1, sy+1, color.Black) // shadow, so it reads on any background
		}
		g.drawTextWith(screen, g.smallFont, county.Name, sx, sy, color.White)
	}
}

// ---------- Drawing ----------

// drawOnMap draws a picture onto the canvas, following the zoom and pan. One of its pixels covers cell km.
func (g *Game) drawOnMap(img *ebiten.Image, cell float64, filter ebiten.Filter) {
	left, top := g.origin()
	op := &ebiten.DrawImageOptions{Filter: filter}
	op.GeoM.Scale(cell, cell)
	op.GeoM.Translate(-left, -top) // move the view's corner to the top-left
	op.GeoM.Scale(g.zoom, g.zoom)  // then enlarge
	g.canvas.DrawImage(img, op)
}

// drawMap puts the background, the layers and the start ring on the canvas.
func (g *Game) drawMap() {
	g.canvas.Fill(OffMap) // anything outside the map

	if g.bgImages[g.bg] == nil {
		g.bgImages[g.bg] = ebiten.NewImageFromImage(g.m.Backgrounds[g.bg].Image) // copy to the graphics card once
	}
	filter := ebiten.FilterNearest // zoomed in, the background's 1 km pixels stay crisp
	if g.zoom < 3 {
		filter = ebiten.FilterLinear // smoothed in the whole-island view
	}
	g.drawOnMap(g.bgImages[g.bg], 1, filter)

	if g.view == dotsView {
		g.drawOnMap(g.shade.img, 1, ebiten.FilterNearest)
	} else {
		g.drawOnMap(g.area.img, areaCell, ebiten.FilterNearest)
	}
	g.drawOnMap(g.tint.img, 1, ebiten.FilterNearest)
	g.canvas.DrawImage(g.borders.img, nil) // county borders, already at screen size

	if g.view == dotsView {
		g.canvas.DrawImage(g.dots.img, nil) // already at screen size
	}
	x, y := g.toScreen(g.world.StartX, g.world.StartY)
	vector.StrokeCircle(g.canvas, x, y, 10, 2, StartMark, true) // a 10-pixel ring where it began
}

// repaintLayers repaints the layers if anything they depend on has changed.
func (g *Game) repaintLayers() {
	state := fmt.Sprintf("%d %p %.4f %.2f %.2f %d %d", g.world.Day, g.world, g.zoom, g.centerX, g.centerY, g.view, g.bg) // everything the layers depend on
	if state == g.drawn {
		return
	}
	if g.view == dotsView {
		g.paintShade()
		g.paintDots()
	} else {
		g.paintArea()
	}
	g.paintTint()
	g.paintBorders()
	g.drawn = state
}

// Draw runs every frame and draws the whole window.
func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.White)
	g.repaintLayers()
	g.drawMap()
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(plotLeft, plotTop) // put the map area in place
	screen.DrawImage(g.canvas, op)
	vector.StrokeRect(screen, plotLeft, plotTop, plotWidth, plotHeight, 1, color.Black, false) // border round the map
	g.drawCountyNames(screen)
	g.drawHospitals(screen)
	g.scaleBar(screen)
	g.drawGraphs(screen) // the graphs beside the map
	g.drawColourKeys(screen)
	g.drawLegend(screen)
	g.drawStatus(screen)
}

// drawColourKeys draws the key for the area colours and for the population map, when they're showing.
func (g *Game) drawColourKeys(screen *ebiten.Image) {
	if g.view != dotsView { // bottom right
		var items []LegendItem
		for i, s := range areaStops {
			label := fmt.Sprintf("%g%%", s.share*100) // e.g. "2%"
			if i == len(areaStops)-1 {
				label += " or more"
			}
			items = append(items, LegendItem{label, areaColour(s.share)})
		}
		g.key(screen, plotLeft+plotWidth-170, viewNames[g.view], "in 10 km squares", items)
	}
	if g.m.Backgrounds[g.bg].Name == "population" { // bottom left
		g.key(screen, plotLeft+16, "People per km²", "2025", populationKey)
	}
}

// drawLegend draws the counts along the top, five to a row.
func (g *Game) drawLegend(screen *ebiten.Image) {
	for i, item := range g.world.Legend() {
		x := 20 + float64(i%legendColumns)*legendWidth
		y := 10 + float64(i/legendColumns)*26
		vector.FillCircle(screen, float32(x+markerRadius), float32(y+10), markerRadius, item.Colour, true)
		g.drawText(screen, item.Label, x+2*markerRadius+6, y, color.Black)
	}
}

// drawStatus draws the status lines and the keys line under the map.
func (g *Game) drawStatus(screen *ebiten.Image) {
	line1, line2, line3 := g.world.Status()
	if g.world.Over() {
		line1 += "   OUTBREAK OVER (R: new outbreak)"
	} else if g.paused {
		line1 += "   PAUSED"
	}
	g.drawText(screen, line1, 20, plotTop+plotHeight+8, color.Black)
	g.drawText(screen, line2, 20, plotTop+plotHeight+30, color.Black)
	g.drawText(screen, line3, 20, plotTop+plotHeight+52, color.Black)
	keys := fmt.Sprintf("Arrows/drag: move  +/−/wheel: zoom  0: whole island  Y: %s rules  B: %s  V: %s  [ ]: %g days/s  Click: seal county  Space: pause  R: restart",
		g.world.Era.Year, g.m.Backgrounds[g.bg].Name, viewNames[g.view], g.speed)
	g.drawText(screen, keys, 20, plotTop+plotHeight+74, Grey) // the keys, in grey
}

// drawHospitals draws each county's isolation hospital in 2026, as in Team-Dolphin-Dog: a white
// square with a red cross and beds used / beds, in red when it is full.
func (g *Game) drawHospitals(screen *ebiten.Image) {
	if !g.world.Era.Modern || !isolateDiagnosed {
		return
	}
	for c := 1; c < len(g.world.Hospitals); c++ {
		h := g.world.Hospitals[c]
		x, y := g.toScreen(h.X, h.Y)
		x, y = x+plotLeft-6, y+plotTop-6             // top-left of a 12-pixel square
		if !onPlot(float64(x), float64(y), 12, 12) { // off the map area
			continue
		}
		fill := color.Color(color.White)
		if h.Patients >= h.Beds {
			fill = color.RGBA{0xff, 0xc0, 0xc0, 0xff} // full: pale red
		}
		vector.FillRect(screen, x, y, 12, 12, fill, false)
		vector.StrokeRect(screen, x, y, 12, 12, 1, Red, false)
		vector.FillRect(screen, x+5, y+2, 2, 8, Red, false) // the cross
		vector.FillRect(screen, x+2, y+5, 8, 2, Red, false)
		if g.zoom > 2*g.fitZoom() { // zoomed in: room for beds used / beds
			label := fmt.Sprintf("%s/%s", thousands(float64(h.Patients)*g.world.PeoplePerDot()), thousands(float64(h.Beds)*g.world.PeoplePerDot()))
			g.drawTextWith(screen, g.smallFont, label, float64(x)+15, float64(y)-1, color.White)
		}
	}
}

// scaleBar draws a "100 km" bar about 150 screen pixels long.
func (g *Game) scaleBar(screen *ebiten.Image) {
	raw := 150 / g.zoom                               // km covered by 150 screen pixels
	nice := math.Pow(10, math.Floor(math.Log10(raw))) // round down to 1, 10, 100…
	for _, f := range []float64{5, 2, 1} {            // then up to the largest 5, 2 or 1 that fits
		if f*nice <= raw {
			nice *= f
			break
		}
	}
	length := float32(nice * g.zoom)                                                             // the bar's length in screen pixels
	x, y := float32(plotLeft+16), float32(plotTop+plotHeight-22)                                 // bottom-left corner of the map
	vector.FillRect(screen, x-6, y-22, length+12, 34, color.RGBA{0xff, 0xff, 0xff, 0xc0}, false) // pale backing
	vector.FillRect(screen, x, y, length, 5, color.Black, false)                                 // the bar
	g.drawText(screen, fmt.Sprintf("%s km", thousands(nice)), float64(x), float64(y)-20, color.Black)
}

// key draws a colour key in the bottom corner of the map area, above the scale bar.
func (g *Game) key(screen *ebiten.Image, x float32, title, foot string, items []LegendItem) {
	const rowH = 22 // pixels per row
	n := float32(len(items))
	y := float32(plotTop+plotHeight-70) - rowH*(n+2)                                                  // top of the key, so its bottom clears the scale bar
	vector.FillRect(screen, x-10, y-6, 164, rowH*(n+2)+12, color.RGBA{0xff, 0xff, 0xff, 0xd0}, false) // pale backing
	g.drawText(screen, title, float64(x), float64(y), color.Black)
	for i, item := range items { // a colour box and its label on each row
		ry := y + rowH*float32(i+1)
		vector.FillRect(screen, x, ry+2, 28, rowH-4, item.Colour, false)
		g.drawText(screen, item.Label, float64(x+38), float64(ry), color.Black)
	}
	g.drawText(screen, foot, float64(x), float64(y+rowH*(n+1)), Grey) // the note underneath, in grey
}

// Legend returns one entry per state for the top of the window, in real people, e.g. "Bubonic=4,200".
func (w *World) Legend() []LegendItem {
	c := w.counts()
	items := make([]LegendItem, 0, numStates)
	for s := range numStates {
		items = append(items, LegendItem{Label: fmt.Sprintf("%s=%s", stateNames[s], thousands(float64(c[s])*w.PeoplePerDot())), Colour: stateColours[s]}) // dots × people per dot
	}
	return items
}

// Status returns the three lines of text under the map.
func (w *World) Status() (string, string, string) {
	dead := w.counts()[Dead]
	// line 1: rules, day, deaths in real people, where it began, and the seed to replay it
	line1 := fmt.Sprintf("%s rules   Day %d   Dead: %s people (%.3f%%)   Began %s   Seed %d", w.Era.Year, w.Day,
		thousands(float64(dead)*w.PeoplePerDot()), 100*float64(dead)/float64(len(w.Agents)), w.StartName, w.Seed)
	if !w.Era.Modern { // 1348 has no response to report
		return line1, "No medicine, no public-health response. 1 dot ≈ " + thousands(w.PeoplePerDot()) + " people.", ""
	}
	line2 := "Response: " + stageNames[w.Stage] // line 2: the response so far
	if w.Stage > Undetected {
		line2 += fmt.Sprintf(" (day %d)", w.StageDay[w.Stage]) // when this stage began
	}
	if next := w.Stage + 1; w.Stage > Undetected && next < numStages {
		line2 += fmt.Sprintf(", %s due day %d", stageNames[next], w.FirstCaseDay+stageStartsAfter[next]) // when the next one is due
	}
	line2 += fmt.Sprintf("   Diagnosed: %s   Saved by tracing: %s   Counties sealed: %d", // diagnosed and saved in real people
		thousands(float64(w.Diagnosed)*w.PeoplePerDot()), thousands(float64(w.Prevented)*w.PeoplePerDot()), w.sealedCount())
	iso := w.IsoHist[len(w.IsoHist)-1] // line 3: isolation and quarantine today, in real people
	p := w.PeoplePerDot()
	line3 := fmt.Sprintf("In hospital: %s of %s beds   Isolating at home: %s   Contacts in quarantine: %s",
		thousands(float64(iso[0])*p), thousands(float64(w.totalBeds())*p), thousands(float64(iso[1])*p), thousands(float64(iso[2])*p))
	return line1, line2, line3
}

// drawText writes s with its top-left corner at (x, y).
func (g *Game) drawText(screen *ebiten.Image, s string, x, y float64, c color.Color) {
	g.drawTextWith(screen, g.font, s, x, y, c)
}

// drawTextWith is drawText in a chosen font.
func (g *Game) drawTextWith(screen *ebiten.Image, face *text.GoTextFace, s string, x, y float64, c color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(c)
	text.Draw(screen, s, face, op)
}

// Layout tells Ebitengine the drawing size; the window scales it to fit when resized.
func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return windowWidth, windowHeight
}

// ---------- Starting the window ----------

// runLive runs the window until it is closed and returns the last simulation. The first
// outbreak uses the given seed.
func runLive(m *Map, seed uint64) *World {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF)) // load the built-in font
	if err != nil {
		log.Fatal(err)
	}

	ebiten.SetWindowSize(windowWidth, windowHeight)
	ebiten.SetWindowTitle("Black Death simulation – Ireland")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled) // the window can be resized

	g := &Game{
		m:         m,
		seed:      seed,
		font:      &text.GoTextFace{Source: src, Size: 15},
		smallFont: &text.GoTextFace{Source: src, Size: 12},
		bgImages:  make([]*ebiten.Image, len(m.Backgrounds)), // one empty slot per background
		speed:     startSpeed,
		canvas:    ebiten.NewImage(plotWidth, plotHeight),
		dots:      newLayer(plotWidth, plotHeight),
		shade:     newLayer(m.W, m.H),
		area:      newLayer((m.W+areaCell-1)/areaCell, (m.H+areaCell-1)/areaCell), // squares across and down, rounding up
		tint:      newLayer(m.W, m.H),
		borders:   newLayer(plotWidth, plotHeight),
	}
	g.resetView()                             // start with the whole island
	g.restart()                               // and a new outbreak
	if err := ebiten.RunGame(g); err != nil { // runs until the window is closed
		log.Fatal(err)
	}
	return g.world // for saving to the CSV
}
