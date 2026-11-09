// The map of Ireland, and the 1348 and 2026 eras.
package main

import (
	"bytes"        // reading the built-in files
	"embed"        // building the map files into the program
	"encoding/csv" // reading the town list
	"fmt"          // error messages and place descriptions
	"image"        // the map images
	"image/jpeg"   // decoding the satellite picture
	"image/png"    // decoding the other maps
	"math"         // distances
	"math/rand/v2" // picking random homes
	"sort"         // finding a weighted random pick quickly
	"strconv"      // town positions from text
	"strings"      // telling .jpg from .png
)

//go:embed maps/*.png maps/*.csv maps/*.jpg
var mapFiles embed.FS // the files in maps/, built into the program so it runs from any folder

// ---------- Eras ----------

// Era holds the settings that depend on when the outbreak happens (Y switches between them).
// Values marked "assumption" are estimates.
type Era struct {
	Year string // shown in the window

	FleaBiteProb  float64 // daily chance of a rat-flea bite, per unit of loose fleas in the area
	EctoProb      float64 // daily chance per sick bubonic neighbour (human fleas and lice)
	PneumonicProb float64 // daily chance per sick pneumonic neighbour

	// Average people infected per sick agent per day, however crowded it is (0 = no limit).
	PneumonicPerDay float64
	EctoPerDay      float64

	DailyKm  float64 // how far from home people go in a day
	TripProb float64 // daily chance of starting a car or train trip
	TripKm   float64 // how far a trip can go

	Modern bool // medicine and a public-health response
}

// Era1348 is the year the Black Death reached Ireland.
var Era1348 = Era{
	Year:         "1348",
	FleaBiteProb: 0.1, EctoProb: 0.3, PneumonicProb: 0.3, // crowded homes, rats and lice everywhere
	PneumonicPerDay: 0.65, // ~1.3 people per case (Gani & Leach, 2004)
	EctoPerDay:      0.3,  // assumption
	DailyKm:         10,   // on foot; no trips
}

// Era2026 is today, with modern housing, hygiene, cars and a public-health response.
var Era2026 = Era{
	Year:          "2026",
	FleaBiteProb:  0.01, // assumption: modern housing and rat control
	EctoProb:      0.03, // assumption: modern hygiene
	PneumonicProb: 0.3,  // breathing it in is as easy as ever

	PneumonicPerDay: 0.65, // same as 1348
	EctoPerDay:      0.05, // assumption

	DailyKm:  24,    // commuting by car or bus
	TripProb: 0.005, // assumption
	TripKm:   300,   // anywhere on the island

	Modern: true,
}

var eras = []*Era{&Era2026, &Era1348} // the window starts with the first one

// ---------- Map settings ----------

// One map pixel is 1 km × 1 km, so distances are in km and in pixels at once.
const (
	pneumonicRadius = 2.0      // pneumonic plague reaches people within 2 km of a sick dot
	ectoRadius      = 2.5      // human fleas and lice reach people within 2.5 km
	ratCell         = 20.0     // rat areas are 20 km across
	ratSpreadRate   = 0.035    // how fast plague spreads from one rat area to the next
	initialSick     = 1        // people ill on day 0
	fixedStart      = "Dublin" // where outbreaks begin when randomStart is off
)

// ---------- Picking places ----------

// picker chooses a pixel at random, weighted by how many people live there.
type picker struct {
	cumulative []float64 // running total of people, pixel by pixel
	pixels     []int32   // the pixel each total belongs to
}

// add puts a pixel and its population on the list.
func (p *picker) add(pixel int, people float64) {
	total := people
	if n := len(p.cumulative); n > 0 {
		total += p.cumulative[n-1] // add to the running total
	}
	p.cumulative = append(p.cumulative, total)
	p.pixels = append(p.pixels, int32(pixel))
}

// pick returns a random pixel; one with twice the people is twice as likely.
func (p *picker) pick(rng *rand.Rand) int {
	r := rng.Float64() * p.cumulative[len(p.cumulative)-1]     // a random person
	return int(p.pixels[sort.SearchFloat64s(p.cumulative, r)]) // the pixel they live in
}

// Town is a named place used to describe where an outbreak began.
type Town struct {
	Name string
	X, Y float64 // km from the top-left corner
}

// County is one of the 32 counties.
type County struct {
	Name           string
	LabelX, LabelY float64 // where its name is drawn, km from the top-left corner
}

const countyDetail = 4 // the border copy of the county map has 4 × 4 pixels per km (250 m)

// Background is one picture B can show under the dots.
type Background struct {
	Name  string // shown in the keys line
	Image image.Image
}

// ---------- Loading the map ----------

// Map is the island: where people live and can go, the towns and the backgrounds.
type Map struct {
	W, H        int          // size in km (pixels)
	land        []bool       // from the mask: where dots may be
	density     []float32    // people per km²
	homes       picker       // picks homes by population
	Towns       []Town       // for naming places
	Backgrounds []Background // satellite, beige map, population
	TotalPeople float64      // everyone on the island

	county     []uint8  // county number for each km of land; 0 = sea
	fineCounty []uint8  // county numbers every 250 m, for drawing smooth borders
	Counties   []County // indexed by county number; Counties[0] is the sea
}

// loadMap reads the built-in map files.
func loadMap() (*Map, error) {
	m := &Map{}
	mask, err := readImage("maps/ireland_mask.png") // black = Irish land
	if err != nil {
		return nil, err
	}
	dens, err := readImage("maps/ireland_population_density_16bit.png") // people per km²
	if err != nil {
		return nil, err
	}
	backgrounds := []struct { // the pictures, each with its on/off setting
		name, file string
		on         bool
	}{
		{"satellite", "satellite.jpg", true},
		{"map", "map.png", mapBackground},
		{"population", "population.png", populationBackground},
	}
	for _, bg := range backgrounds {
		if !bg.on { // switched off in main.go
			continue
		}
		img, err := readImage("maps/ireland_" + bg.file)
		if err != nil {
			return nil, err
		}
		m.Backgrounds = append(m.Backgrounds, Background{Name: bg.name, Image: img})
	}

	b := mask.Bounds()
	m.W, m.H = b.Dx(), b.Dy()
	m.land = make([]bool, m.W*m.H)
	m.density = make([]float32, m.W*m.H)
	maskGray, ok1 := mask.(*image.Gray)   // one byte per pixel
	densGray, ok2 := dens.(*image.Gray16) // two bytes per pixel
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("unexpected image format")
	}
	for i := range m.land { // every pixel
		m.land[i] = maskGray.Pix[i] == 0                                             // black = land
		m.density[i] = float32(int(densGray.Pix[2*i])<<8 | int(densGray.Pix[2*i+1])) // 16-bit, big-endian
		if m.land[i] && m.density[i] > 0 {                                           // people live here
			m.homes.add(i, float64(m.density[i])) // so it can be picked as a home
			m.TotalPeople += float64(m.density[i])
		}
	}
	if err := m.loadCounties(); err != nil {
		return nil, err
	}
	return m, m.loadTowns()
}

// loadTowns reads the town list.
func (m *Map) loadTowns() error {
	data, err := mapFiles.ReadFile("maps/ireland_towns.csv")
	if err != nil {
		return err
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		return err
	}
	for _, row := range rows[1:] { // skip the header; columns: name, x, y
		t := Town{Name: row[0]}
		t.X, _ = strconv.ParseFloat(row[1], 64)
		t.Y, _ = strconv.ParseFloat(row[2], 64)
		m.Towns = append(m.Towns, t)
	}
	return nil
}

// loadCounties reads the county numbers (1 km and 250 m) and the county names.
func (m *Map) loadCounties() error {
	coarse, err := readImage("maps/ireland_counties.png") // one byte per km: the county number
	if err != nil {
		return err
	}
	fine, err := readImage("maps/ireland_counties_250m.png") // the same at 250 m
	if err != nil {
		return err
	}
	m.county = coarse.(*image.Gray).Pix
	m.fineCounty = fine.(*image.Gray).Pix
	data, err := mapFiles.ReadFile("maps/ireland_counties.csv")
	if err != nil {
		return err
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		return err
	}
	m.Counties = make([]County, len(rows)) // the header row's slot becomes Counties[0], the sea
	for _, row := range rows[1:] {         // columns: id, name, label_x, label_y
		id, _ := strconv.Atoi(row[0])
		c := &m.Counties[id]
		c.Name = row[1]
		c.LabelX, _ = strconv.ParseFloat(row[2], 64)
		c.LabelY, _ = strconv.ParseFloat(row[3], 64)
	}
	return nil
}

// readImage decodes one of the built-in pictures.
func readImage(name string) (image.Image, error) {
	data, err := mapFiles.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(name, ".jpg") {
		return jpeg.Decode(bytes.NewReader(data))
	}
	return png.Decode(bytes.NewReader(data))
}

// ---------- Places on the map ----------

// IsLand reports whether a position is on the island.
func (m *Map) IsLand(x, y float64) bool {
	if x < 0 || y < 0 || x >= float64(m.W) || y >= float64(m.H) { // off the map
		return false
	}
	return m.land[int(y)*m.W+int(x)]
}

// CountyAt returns the county number at a position; 0 = sea or off the map.
func (m *Map) CountyAt(x, y float64) int {
	if x < 0 || y < 0 || x >= float64(m.W) || y >= float64(m.H) {
		return 0
	}
	return int(m.county[int(y)*m.W+int(x)])
}

// fineCountyAt is CountyAt at 250 m detail, used to draw the borders.
func (m *Map) fineCountyAt(x, y float64) int {
	fx, fy := int(x*countyDetail), int(y*countyDetail) // the 250 m pixel
	if x < 0 || y < 0 || fx >= m.W*countyDetail || fy >= m.H*countyDetail {
		return 0
	}
	return int(m.fineCounty[fy*m.W*countyDetail+fx])
}

// RandomHome picks a random spot in a populated place, weighted by population.
func (m *Map) RandomHome(rng *rand.Rand) (float64, float64) {
	p := m.homes.pick(rng)
	return float64(p%m.W) + rng.Float64(), float64(p/m.W) + rng.Float64() // somewhere inside that km square
}

// TripDestination picks a populated place within maxDist, or false if none was found.
func (m *Map) TripDestination(rng *rand.Rand, x, y, maxDist float64) (float64, float64, bool) {
	for try := 0; try < 20; try++ { // up to 20 tries
		dx, dy := m.RandomHome(rng)
		if (dx-x)*(dx-x)+(dy-y)*(dy-y) <= maxDist*maxDist { // close enough
			return dx, dy, true
		}
	}
	return 0, 0, false
}

// Town finds a town by name.
func (m *Map) Town(name string) Town {
	for _, t := range m.Towns {
		if t.Name == name {
			return t
		}
	}
	panic("no town called " + name) // a typo in fixedStart
}

// Describe names a position by the nearest town, e.g. "near Kilkenny" or "25 km from Athlone".
func (m *Map) Describe(x, y float64) string {
	best, bestD := "", math.Inf(1)
	for _, t := range m.Towns {
		if d := math.Hypot(t.X-x, t.Y-y); d < bestD { // closer than any town so far
			best, bestD = t.Name, d
		}
	}
	if bestD <= 8 { // within 8 km counts as "near"
		return "near " + best
	}
	return fmt.Sprintf("%.0f km from %s", bestD, best)
}

// RatsIn is an area's starting rat population (0 to 1). Rats live where people live: 1,000 or
// more people per km² counts as full.
func (m *Map) RatsIn(x0, y0, x1, y1 int) float64 {
	sum, n := 0.0, 0
	for y := y0; y < y1 && y < m.H; y++ {
		for x := x0; x < x1 && x < m.W; x++ {
			n++ // every km² in the area, land or sea
			if m.land[y*m.W+x] {
				sum += float64(m.density[y*m.W+x]) // people living there
			}
		}
	}
	if n == 0 || sum == 0 { // nobody lives here, so no rats
		return 0
	}
	return math.Min(1, math.Log10(1+sum/float64(n))/3) // 10 people/km² → ⅓ full, 100 → ⅔, 1,000 → full
}
