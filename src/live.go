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
	Grey      = color.RGBA{0x55, 0x55, 0x55, 0xff} // dead (legend only; the dead aren't drawn)
	Brown     = color.RGBA{0x8c, 0x5a, 0x28, 0xff} // plague in the rats and fleas
	OffMap    = color.RGBA{0x20, 0x20, 0x20, 0xff} // around the edges of the map
	StartMark = color.RGBA{0xff, 0xd7, 0x00, 0xff} // the ring where the outbreak began
	Border    = color.RGBA{0xff, 0xff, 0xff, 0xa0} // county borders on the satellite: see-through white
	DarkLine  = color.RGBA{0x30, 0x30, 0x30, 0xd0} // county borders on the paler maps: see-through grey
	SealedRed = color.RGBA{0xd0, 0x10, 0x10, 0xff} // sealed counties' tint and name labels
)

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
	windowWidth   = 1380 // the map, then the graphs (graphs.go) on the right
	windowHeight  = 922
	plotLeft      = 20  // the map area's left edge
	plotTop       = 70  // the map area's top edge, below the legend
	plotWidth     = 960 // the map area's size
	plotHeight    = 740
	markerRadius  = 4   // legend dots
	legendColumns = 5   // legend entries per row
	legendWidth   = 196 // width of each legend entry
	maxZoom       = 16  // closest zoom: screen pixels per km
	panSpeed      = 10  // screen pixels moved per frame with the arrow keys

	areaCell    = 10   // area-view squares, km
	areaMinDots = 5    // squares with fewer dots are left blank; a share of 1–4 dots is noise
	areaAlpha   = 0.75 // see-through, so the map shows underneath
)

var stepsPerSecond = 3.5 // simulated days per second; [ and ] halve or double it

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

	centerX, centerY float64 // map position in the middle of the view
	zoom             float64 // screen pixels per km

	// Layers over the background. They are repainted only when something changes; Ebitengine
	// scales and combines them every frame.
	canvas   *ebiten.Image // the map area, put together each frame
	dots     *ebiten.Image // one pixel per screen pixel
	dotPix   []byte        // the dots' pixels before they are sent to dots
	shade    *ebiten.Image // rat shading, one pixel per km
	shadePix []byte
	area     *ebiten.Image // area view, one pixel per area square
	areaPix  []byte
	tint     *ebiten.Image // red over sealed counties, one pixel per km
	tintPix  []byte
	borders  *ebiten.Image // county borders, one pixel per screen pixel
	bordPix  []byte
	drawn    string // what the layers show, to skip repainting when nothing changed

	mouseX, mouseY int  // pointer position last frame, for dragging
	dragging       bool // the mouse button was already down last frame
	pressX, pressY int  // where the mouse button went down, to tell a click from a drag
}

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
	switch { // keys that act once per press
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
		stepsPerSecond = math.Min(stepsPerSecond*2, 112) // twice as fast, up to 112 days a second
	case inpututil.IsKeyJustPressed(ebiten.KeyBracketLeft):
		stepsPerSecond = math.Max(stepsPerSecond/2, 0.875) // half as fast
	}

	step := panSpeed / g.zoom // the same screen distance at any zoom
	if ebiten.IsKeyPressed(ebiten.KeyArrowLeft) {
		g.centerX -= step
	}
	if ebiten.IsKeyPressed(ebiten.KeyArrowRight) {
		g.centerX += step
	}
	if ebiten.IsKeyPressed(ebiten.KeyArrowUp) {
		g.centerY -= step
	}
	if ebiten.IsKeyPressed(ebiten.KeyArrowDown) {
		g.centerY += step
	}
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

	// +/− zoom on the middle of the map; the wheel zooms on the pointer
	if ebiten.IsKeyPressed(ebiten.KeyEqual) || ebiten.IsKeyPressed(ebiten.KeyNumpadAdd) {
		g.zoomBy(1.04, plotLeft+plotWidth/2, plotTop+plotHeight/2) // 4% closer each frame the key is held
	}
	if ebiten.IsKeyPressed(ebiten.KeyMinus) || ebiten.IsKeyPressed(ebiten.KeyNumpadSubtract) {
		g.zoomBy(1/1.04, plotLeft+plotWidth/2, plotTop+plotHeight/2)
	}
	if _, wheel := ebiten.Wheel(); wheel != 0 {
		g.zoomBy(math.Pow(1.15, wheel), float64(mx), float64(my)) // 15% per wheel step
	}
	g.clampView() // don't let the view leave the map

	if !g.paused && !g.world.Over() { // the days stop once nobody is infected and the rats are clear
		g.stepTimer += stepsPerSecond / 60                               // this frame's share of a day
		for n := 0; g.stepTimer >= 1 && n < 20 && !g.world.Over(); n++ { // run each whole day that is due, at most 20 a frame
			g.world.Step()
			g.stepTimer--
		}
	}
	return nil
}

// click seals or opens the county under screen position (sx, sy).
func (g *Game) click(sx, sy int) {
	if sx < plotLeft || sy < plotTop || sx >= plotLeft+plotWidth || sy >= plotTop+plotHeight { // not on the map
		return
	}
	left, top := g.origin()
	x, y := left+float64(sx-plotLeft)/g.zoom, top+float64(sy-plotTop)/g.zoom // screen to map (km)
	if c := g.m.CountyAt(x, y); c > 0 {
		g.world.toggleSeal(c)
		g.drawn = "" // repaint the red tint
	}
}

// abs is the size of n, without its sign.
func abs(n int) int {
	return max(n, -n)
}

// zoomBy magnifies by factor, keeping the map point under screen position (sx, sy) in place.
func (g *Game) zoomBy(factor, sx, sy float64) {
	before := g.zoom
	g.zoom = math.Max(g.fitZoom(), math.Min(g.zoom*factor, maxZoom)) // no further out than the whole island, no closer than maxZoom
	ox, oy := sx-(plotLeft+plotWidth/2), sy-(plotTop+plotHeight/2)   // the point's distance from the middle of the view
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
	if g.shade == nil { // first time: make the layer
		g.shade = ebiten.NewImage(m.W, m.H)
		g.shadePix = make([]byte, m.W*m.H*4)
	}
	for i, land := range m.land { // every km of the map
		shade := 0.0 // sea stays clear
		if land {
			cx, cy := w.cellOf(float64(i%m.W), float64(i/m.W)) // the rat area this km is in
			c := w.Cells[cy*w.GridW+cx]
			shade = min(1, (c.Infected+c.Fleas)*3) * 0.55 // stronger with more plague, at most 55%
		}
		setPixel(g.shadePix, i, Brown, shade)
	}
	g.shade.WritePixels(g.shadePix) // send to the graphics card
}

// paintDots draws one dot per living agent. Dots grow as you zoom in; the dead aren't drawn.
func (g *Game) paintDots() {
	if g.dots == nil { // first time: make the layer
		g.dots = ebiten.NewImage(plotWidth, plotHeight)
		g.dotPix = make([]byte, plotWidth*plotHeight*4)
	}
	clear(g.dotPix) // start see-through
	r := 0          // dot radius: 0 is a single pixel
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
					setPixel(g.dotPix, y*plotWidth+x, stateColours[a.State], 1)
				}
			}
		}
	}
	g.dots.WritePixels(g.dotPix) // send to the graphics card
}

// paintArea colours each 10 km square by the share of its dots that are infected now (caught
// it or ill) or dead. Dots are counted where they are; the dead stay where they died.
func (g *Game) paintArea() {
	cols := (g.m.W + areaCell - 1) / areaCell // squares across, rounding up
	rows := (g.m.H + areaCell - 1) / areaCell // squares down
	if g.area == nil {                        // first time: make the layer
		g.area = ebiten.NewImage(cols, rows)
		g.areaPix = make([]byte, cols*rows*4)
	}
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
	clear(g.areaPix) // start see-through
	for i := range total {
		if total[i] >= areaMinDots && hit[i] > 0 { // enough dots to trust, and something to show
			setPixel(g.areaPix, i, areaColour(float64(hit[i])/float64(total[i])), areaAlpha)
		}
	}
	g.area.WritePixels(g.areaPix) // send to the graphics card
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
	m := g.m
	if g.tint == nil { // first time: make the layer
		g.tint = ebiten.NewImage(m.W, m.H)
		g.tintPix = make([]byte, m.W*m.H*4)
	}
	for i, c := range m.county { // every km of the map
		alpha := 0.0
		if g.world.Sealed[c] && c != 0 { // land in a sealed county
			alpha = 0.22
		}
		setPixel(g.tintPix, i, SealedRed, alpha)
	}
	g.tint.WritePixels(g.tintPix)
}

// paintBorders draws a one-pixel line wherever neighbouring screen pixels are in different
// counties, so the lines stay thin at any zoom.
func (g *Game) paintBorders() {
	if g.borders == nil { // first time: make the layer
		g.borders = ebiten.NewImage(plotWidth, plotHeight)
		g.bordPix = make([]byte, plotWidth*plotHeight*4)
	}
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
	clear(g.bordPix) // start see-through
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
			setPixel(g.bordPix, i, color.RGBA{line.R, line.G, line.B, 0xff}, float64(line.A)/255)
		}
	}
	g.borders.WritePixels(g.bordPix)
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
		sx, sy := float64(x)+plotLeft-w/2, float64(y)+plotTop-h/2                                    // centred on the label point
		if sx < plotLeft || sy < plotTop || sx+w > plotLeft+plotWidth || sy+h > plotTop+plotHeight { // off the map area
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

// drawMap puts the background, the layers and the start ring on the canvas.
func (g *Game) drawMap() {
	left, top := g.origin()
	g.canvas.Fill(OffMap) // anything outside the map

	// The background is smoothed in the whole-island view; zoomed in, its 1 km pixels stay crisp.
	if g.bgImages[g.bg] == nil {
		g.bgImages[g.bg] = ebiten.NewImageFromImage(g.m.Backgrounds[g.bg].Image) // copy to the graphics card once
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(-left, -top) // move the view's corner to the top-left
	op.GeoM.Scale(g.zoom, g.zoom)  // then enlarge
	if g.zoom < 3 {
		op.Filter = ebiten.FilterLinear // smooth
	}
	g.canvas.DrawImage(g.bgImages[g.bg], op)

	layer, cell := g.shade, 1.0 // dots view: rat shading, one pixel per km
	if g.view != dotsView {
		layer, cell = g.area, areaCell // area views: one pixel per 10 km square
	}
	op = &ebiten.DrawImageOptions{}
	op.GeoM.Scale(cell, cell) // one layer pixel per km or per area square
	op.GeoM.Translate(-left, -top)
	op.GeoM.Scale(g.zoom, g.zoom)
	g.canvas.DrawImage(layer, op)

	op = &ebiten.DrawImageOptions{}
	op.GeoM.Translate(-left, -top) // sealed-county tint, one pixel per km
	op.GeoM.Scale(g.zoom, g.zoom)
	g.canvas.DrawImage(g.tint, op)
	g.canvas.DrawImage(g.borders, nil) // county borders, already at screen size

	if g.view == dotsView {
		g.canvas.DrawImage(g.dots, nil) // already at screen size
	}
	x, y := g.toScreen(g.world.StartX, g.world.StartY)
	vector.StrokeCircle(g.canvas, x, y, 10, 2, StartMark, true) // a 10-pixel ring where it began
}

// Draw runs every frame and draws the whole window.
func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.White)
	state := fmt.Sprintf("%d %p %.4f %.2f %.2f %d %d", g.world.Day, g.world, g.zoom, g.centerX, g.centerY, g.view, g.bg) // everything the layers depend on
	if state != g.drawn {                                                                                                // something changed, so repaint
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
	g.drawMap()
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(plotLeft, plotTop) // put the map area in place
	screen.DrawImage(g.canvas, op)
	vector.StrokeRect(screen, plotLeft, plotTop, plotWidth, plotHeight, 1, color.Black, false) // border round the map
	g.drawCountyNames(screen)
	g.drawHospitals(screen)
	g.scaleBar(screen)
	g.drawGraphs(screen) // the graphs beside the map

	if g.view != dotsView { // key for the area colours, bottom right
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
	if g.m.Backgrounds[g.bg].Name == "population" { // key for the population map, bottom left
		g.key(screen, plotLeft+16, "People per km²", "2025", populationKey)
	}

	for i, item := range g.world.Legend() { // the counts along the top, five to a row
		x := 20 + float64(i%legendColumns)*legendWidth
		y := 10 + float64(i/legendColumns)*26
		vector.FillCircle(screen, float32(x+markerRadius), float32(y+10), markerRadius, item.Colour, true)
		g.drawText(screen, item.Label, x+2*markerRadius+6, y, color.Black)
	}

	line1, line2, line3 := g.world.Status() // the status lines under the map
	if g.world.Over() {
		line1 += "   OUTBREAK OVER (R: new outbreak)"
	} else if g.paused {
		line1 += "   PAUSED"
	}
	g.drawText(screen, line1, 20, plotTop+plotHeight+8, color.Black)
	g.drawText(screen, line2, 20, plotTop+plotHeight+30, color.Black)
	g.drawText(screen, line3, 20, plotTop+plotHeight+52, color.Black)
	keys := fmt.Sprintf("Arrows/drag: move  +/−/wheel: zoom  0: whole island  Y: %s rules  B: %s  V: %s  [ ]: %g days/s  Click: seal county  Space: pause  R: restart",
		g.world.Era.Year, g.m.Backgrounds[g.bg].Name, viewNames[g.view], stepsPerSecond)
	g.drawText(screen, keys, 20, plotTop+plotHeight+74, color.RGBA{0x55, 0x55, 0x55, 0xff}) // the keys, in grey
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
		x, y = x+plotLeft-6, y+plotTop-6                                                           // top-left of a 12-pixel square
		if x < plotLeft || y < plotTop || x+12 > plotLeft+plotWidth || y+12 > plotTop+plotHeight { // off the map area
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
	g.drawText(screen, foot, float64(x), float64(y+rowH*(n+1)), color.RGBA{0x55, 0x55, 0x55, 0xff}) // the note underneath, in grey
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
		canvas:    ebiten.NewImage(plotWidth, plotHeight),
	}
	g.resetView()                             // start with the whole island
	g.restart()                               // and a new outbreak
	if err := ebiten.RunGame(g); err != nil { // runs until the window is closed
		log.Fatal(err)
	}
	return g.world // for saving to the CSV
}
