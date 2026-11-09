// Black Death simulation for the island of Ireland: medieval (1348) vs modern (2026) conditions.
package main

import (
	"encoding/csv" // writing plague.csv
	"flag"         // reading -batch, -seed and -cores from the command line
	"fmt"          // building text for the screen and the terminal
	"image/color"  // dot colours
	"log"          // stopping with an error message
	"math"         // rounding, distances, min and max
	"math/rand/v2" // all the chance parts of the model
	"os"           // creating the CSV file
	"runtime"      // how many cores this machine has
	"slices"       // copying the rat areas each day
	"strconv"      // numbers to text for the CSV
	"sync"         // the lock on hospital beds
	"time"         // timing batch runs
)

// ---------- Settings ----------

// The 2026 measures. They have no effect on the 1348 rules.
const (
	isolateDiagnosed   = true // diagnosed people go to an isolation hospital, or isolate at home when beds run out (see quarantine.go)
	quarantineContacts = true // traced close contacts stay at home for 7 days and are checked daily
	contactTracing     = true // close contacts of pneumonic cases get preventive antibiotics
	socialDistancing   = true // people mix less once emergencies are declared
	travelCuts         = true // fewer trips once emergencies are declared
	countyLockdowns    = true // counties seal when 0.5% of their people are ill (see updateLockdowns)
)

// Extras.
const (
	mapBackground        = true // beige map, selectable with B
	populationBackground = true // population map, selectable with B
	saveCSV              = true // write plague.csv when the window closes
	batchMode            = true // allow `go run ./src -batch N`
	randomStart          = true // false: always start in Dublin
)

const numAgents = 200000 // number of dots; each stands for ~35 people

// ---------- Disease and rats ----------

const (
	sickSpeed = 0.25 // ill people move only a quarter as far each day (assumption)

	useRats      = true // plague spreads among rats and their fleas bite people
	useEcto      = true // human fleas and lice spread bubonic plague between people
	usePneumonic = true // pneumonic plague spreads through the air between people

	bubonicDeathProb     = 0.5  // untreated bubonic plague kills 30–60% (WHO)
	bubonicPneumonicProb = 0.1  // untreated bubonic cases that spread to the lungs (assumption)
	pneumonicDeathProb   = 0.99 // untreated pneumonic plague is almost always fatal (WHO)

	ratInfectRate  = 0.6  // how fast plague spreads among rats in one area (assumption)
	ratDeathRate   = 0.25 // share of infected rats dying each day (assumption)
	fleasPerRat    = 1.0  // infected fleas set loose by each dead rat (assumption)
	fleaDecay      = 0.1  // share of loose fleas dying each day without a host (assumption)
	startInfection = 0.05 // share of rats infected where the outbreak begins

	tripDaysMin, tripDaysMax = 1, 7 // a trip lasts 1 to 7 days (assumption)

	maxDays = 3650         // batch runs stop after 10 years
	csvFile = "plague.csv" // where the daily counts are saved
)

// ---------- Health states ----------

// State is where a person is in the illness.
type State uint8

const (
	Susceptible      State = iota // healthy and can catch it
	ExposedBubonic                // caught bubonic plague, not ill yet
	ExposedPneumonic              // caught pneumonic plague, not ill yet
	Bubonic                       // ill with bubonic plague
	Pneumonic                     // ill with pneumonic plague, infectious through the air
	Recovered                     // survived and immune
	Dead
	numStates // how many states there are, for sizing arrays
)

var stateNames = [numStates]string{"Susceptible", "Exposed B", "Exposed P", "Bubonic", "Pneumonic", "Recovered", "Dead"} // legend text
var stateColours = [numStates]color.RGBA{Green, Orange, Yellow, Red, Purple, Blue, Grey}                                 // dot colours

// stateDays is the shortest and longest stay in each state; {0, 0} means it lasts until changed.
var stateDays = [numStates][2]int{
	ExposedBubonic:   {2, 8},  // incubation (CDC)
	ExposedPneumonic: {1, 3},  // incubation (CDC)
	Bubonic:          {4, 10}, // illness (assumption)
	Pneumonic:        {1, 3},  // illness; it can kill within a day (WHO)
}

// ---------- People ----------

// Agent is one dot. Positions are in km (1 km map pixels) from the top-left corner.
type Agent struct {
	X, Y          float64 // where they are today
	HomeX, HomeY  float64 // where they live
	BaseX, BaseY  float64 // what they move around each day: home, or the destination while on a trip
	AwayDays      int     // days left on a trip; 0 = at home
	State         State   // healthy, incubating, ill, recovered or dead
	Timer         int     // days left in the current state
	SeekDay       int     // day they go to a doctor; 1<<30 = never
	Treated       bool    // diagnosed and on antibiotics
	Hospital      int     // the county whose isolation hospital they're in; 0 = not in hospital
	QuarantineEnd int     // the day their quarantine as a close contact ends; 0 = never quarantined
}

// Become moves a person to a new state and sets how long they stay in it.
func (a *Agent) Become(s State, rng *rand.Rand) {
	a.State = s
	a.Timer = 0                      // no time limit unless the state has one
	if d := stateDays[s]; d[1] > 0 { // this state has a time limit
		a.Timer = d[0] + rng.IntN(d[1]-d[0]+1) // a random number of days between the shortest and longest
	}
}

func (a *Agent) sick() bool               { return a.State == Bubonic || a.State == Pneumonic } // ill with either form
func (a *Agent) isolated() bool           { return a.Treated && isolateDiagnosed }              // diagnosed and kept apart, in hospital or at home
func (a *Agent) quarantined(day int) bool { return day < a.QuarantineEnd }                      // a close contact staying at home

// Move puts a person somewhere near their base for the day, usually close and at most DailyKm
// away. A spot that isn't land in the mask, or is across a sealed county border, is rejected,
// which keeps everyone on the island and inside sealed counties.
func (a *Agent) Move(w *World, rng *rand.Rand) {
	m, era := w.Map, w.Era
	if a.State == Dead || a.isolated() || a.quarantined(w.Day) { // the dead, the isolated and the quarantined stay put
		return
	}
	r := era.DailyKm // the furthest anyone goes in a day
	spread := r / 3  // most days are spent much closer to the base
	if a.sick() {
		spread *= sickSpeed // ill people stay closer still
	}
	x := a.BaseX + math.Max(-r, math.Min(rng.NormFloat64()*spread, r)) // random east–west step, capped at r
	y := a.BaseY + math.Max(-r, math.Min(rng.NormFloat64()*spread, r)) // random north–south step, capped at r
	if m.IsLand(x, y) && !w.blocked(a.BaseX, a.BaseY, x, y) {          // only onto land, and not across a sealed border; otherwise stay put
		a.X, a.Y = x, y
	}
}

// goTo sends a person on a trip for some days, or moves them for good if days is 0.
func (a *Agent) goTo(x, y float64, days int) {
	a.X, a.Y = x, y         // they arrive straight away
	a.BaseX, a.BaseY = x, y // and move around the new place each day
	a.AwayDays = days       // until the trip ends
	if days == 0 {
		a.HomeX, a.HomeY = x, y // moving for good: this is now home
	}
}

// ---------- Rats and fleas ----------

// Cell is one 20 km area of rats. Values are shares of a full area of rats.
type Cell struct {
	Rats       float64 // healthy rats; fewer where fewer people live
	Infected   float64 // rats carrying plague
	Fleas      float64 // infected fleas whose rat has died, looking for a new host
	ControlDay int32   // day pest control starts; -1 = none
}

// ---------- The world ----------

// World is one outbreak: everyone, every rat area, and the response so far.
type World struct {
	Map          *Map    // where people live and can go
	Era          *Era    // 1348 or 2026 rules
	Agents       []Agent // all the dots
	Cells        []Cell  // all the rat areas
	GridW, GridH int     // rat areas across and down
	Day          int     // days since the outbreak began

	StartX, StartY float64 // where the outbreak began
	StartName      string  // e.g. "near Kilkenny"

	Stage        Stage          // how far the response has got
	StageDay     [numStages]int // the day each stage began
	FirstCaseDay int            // day of the first diagnosis; -1 = not yet
	Diagnosed    int            // people diagnosed so far
	Prevented    int            // contacts saved by preventive antibiotics

	LastCase       []int      // for each county, the day of its last diagnosis; -1 = none yet
	LastIslandCase int        // the day of the last diagnosis anywhere; -1 = none yet
	Sealed         []bool     // for each county, whether its border is closed today
	Hospitals      []Hospital // each county's isolation hospital; Hospitals[0], the sea, has none
	Manual         []bool     // for each county, whether it was sealed or opened by a click

	History    [][numStates]int // counts of each state for every day, for the CSV
	StageHist  []Stage          // the response stage for every day, for the CSV
	SealedHist []int            // counties sealed on every day, for the CSV
	IsoHist    [][3]int         // dots in hospital, isolating at home and quarantined on every day
	Seed       uint64           // the same seed always gives the same outbreak (see parallel.go)
	bedLock    sync.Mutex       // hospital beds are freed from several cores at once (see discharge)
	sick       sickGrid         // reused each day to find sick people quickly
}

// newWorld sets up a new outbreak: people at home, rats where people live, and the first case.
func newWorld(m *Map, era *Era, seed uint64) *World {
	w := &World{Map: m, Era: era, Agents: make([]Agent, numAgents), FirstCaseDay: -1, LastIslandCase: -1, Seed: seed}
	w.LastCase = make([]int, len(m.Counties))            // one per county
	w.Sealed = make([]bool, len(m.Counties))             // all open at the start
	w.Manual = make([]bool, len(m.Counties))             // none clicked yet
	w.Hospitals = m.hospitals(m.TotalPeople / numAgents) // every bed free
	for c := range w.LastCase {
		w.LastCase[c] = -1 // no cases yet
	}
	w.forBlocks(stepSetup, func(lo, hi int, rng *rand.Rand) {
		for i := lo; i < hi; i++ {
			x, y := m.RandomHome(rng)                                               // a home picked by population
			w.Agents[i] = Agent{X: x, Y: y, HomeX: x, HomeY: y, BaseX: x, BaseY: y} // healthy and at home
		}
	})

	rng := newStream(seed, 0, stepStart, 0) // its own random numbers for the start
	start := m.Town(fixedStart)
	w.StartX, w.StartY = start.X, start.Y // Dublin, unless randomStart is on
	if randomStart {
		w.StartX, w.StartY = m.RandomHome(rng) // anywhere, more likely where more people live
	}
	w.StartName = m.Describe(w.StartX, w.StartY) // e.g. "near Kilkenny"
	for i := 0; i < initialSick; i++ {           // the first sick person (or people)
		a := &w.Agents[i]
		a.goTo(w.StartX, w.StartY, 0)    // move them to the start
		for try := 0; try < 100; try++ { // then somewhere nearby, inside the infected rat areas
			x := w.StartX + uniform(rng, -1.5, 1.5)*ratCell // within 1.5 rat areas east or west
			y := w.StartY + uniform(rng, -1, 1)*ratCell     // within 1 rat area north or south
			if m.IsLand(x, y) {
				a.goTo(x, y, 0)
				break
			}
		}
		a.Become(Bubonic, rng) // already ill
		w.seekCare(a, rng)     // and will see a doctor some day
	}

	w.GridW = int(math.Ceil(float64(m.W) / ratCell)) // rat areas across the map
	w.GridH = int(math.Ceil(float64(m.H) / ratCell)) // rat areas down the map
	w.Cells = make([]Cell, w.GridW*w.GridH)
	for cy := 0; cy < w.GridH; cy++ {
		for cx := 0; cx < w.GridW; cx++ {
			x0, y0 := int(float64(cx)*ratCell), int(float64(cy)*ratCell)                  // the area's top-left km
			x1, y1 := int(float64(cx+1)*ratCell), int(float64(cy+1)*ratCell)              // and bottom-right km
			w.Cells[cy*w.GridW+cx] = Cell{Rats: m.RatsIn(x0, y0, x1, y1), ControlDay: -1} // rats by population, no pest control
		}
	}
	if useRats {
		hx, hy := w.cellOf(w.StartX, w.StartY) // the rat area where the outbreak begins
		for dx := -1; dx <= 1; dx++ {          // it and its neighbours east and west
			if hx+dx >= 0 && hx+dx < w.GridW { // stay on the map
				cell := &w.Cells[hy*w.GridW+hx+dx]
				infected := startInfection * math.Max(cell.Rats, 0.5) // even where few rats live
				cell.Rats = math.Max(cell.Rats-infected, 0)           // those rats are no longer healthy
				cell.Infected = infected
			}
		}
	}
	w.record() // day 0 counts
	return w
}

// cellOf returns the rat area (column, row) containing a position.
func (w *World) cellOf(x, y float64) (int, int) {
	cx := min(int(x/ratCell), w.GridW-1)
	cy := min(int(y/ratCell), w.GridH-1)
	return max(cx, 0), max(cy, 0) // kept on the map
}

// Step advances one day. New infections are decided from the start-of-day state and applied
// afterwards, so the order of the agents doesn't affect who gets infected. The slowest steps
// (travel, catching plague, illnesses moving on) are shared between the cores (see parallel.go).
func (w *World) Step() {
	w.travel() // everyone moves; trips start and end
	if useRats {
		w.updateRats() // plague spreads among rats; dying rats release fleas
	}
	caught := w.newExposures() // who catches plague today, one list per block
	w.forBlocks(stepProgress, func(lo, hi int, rng *rand.Rand) {
		for i := lo; i < hi; i++ {
			w.progress(&w.Agents[i], rng) // illnesses move on: fall ill, die or recover
		}
		for _, c := range caught[lo/blockSize] { // they were healthy, so progress left them alone
			w.Agents[c.agent].Become(c.state, rng) // today's new infections take hold
		}
	})
	if w.Era.Modern {
		w.diagnose() // doctors diagnose and treat; tracing and pest control start
	}
	w.Day++
	w.updateStage()     // the response may move to its next stage
	w.updateLockdowns() // counties seal and reopen
	w.record()          // save today's counts
}

// progress ends a state when its timer runs out. A sick person dies, recovers, or (untreated
// bubonic only) turns pneumonic.
func (w *World) progress(a *Agent, rng *rand.Rand) {
	if stateDays[a.State][1] == 0 { // healthy, recovered or dead: nothing to count down
		return
	}
	a.Timer--
	if a.Timer > 0 { // still days left
		return
	}
	switch a.State {
	case ExposedBubonic: // incubation is over
		a.Become(Bubonic, rng)
		w.seekCare(a, rng)
		return
	case ExposedPneumonic:
		a.Become(Pneumonic, rng)
		w.seekCare(a, rng)
		return
	}
	defer w.discharge(a)  // whatever happens next, a hospital bed is freed
	roll := rng.Float64() // one roll decides how the illness ends
	switch {
	case roll < deathChance(a):
		a.Become(Dead, rng)
	case a.State == Bubonic && !a.Treated && roll < bubonicDeathProb+bubonicPneumonicProb: // the next 10% of rolls
		a.Become(Pneumonic, rng) // the infection reaches the lungs
		return                   // still ill, and untreated, so never in hospital
	default:
		a.Become(Recovered, rng)
	}
	a.Treated = false // the illness is over
}

// ---------- Travel ----------

// travel handles daily movement, returning from trips and starting new ones. People with symptoms
// don't travel, but people still incubating do, and can carry plague with them.
func (w *World) travel() {
	m, era := w.Map, w.Era
	trip := era.TripProb * w.travelShare() // today's chance of starting a trip, after any travel cuts
	w.forBlocks(stepTravel, func(lo, hi int, rng *rand.Rand) {
		for i := lo; i < hi; i++ { // each dot only changes itself, so blocks can run at once
			a := &w.Agents[i]
			if a.AwayDays > 0 { // on a trip
				a.AwayDays--
				if a.AwayDays == 0 && !a.isolated() && a.State != Dead { // the trip is over
					a.goTo(a.HomeX, a.HomeY, 0) // go home
				}
			}
			a.Move(w, rng) // today's local movement

			if a.sick() || a.State == Dead || a.Treated || a.AwayDays > 0 || a.quarantined(w.Day) { // can't start a trip
				continue
			}
			if rng.Float64() < trip {
				if x, y, ok := m.TripDestination(rng, a.X, a.Y, era.TripKm); ok && !w.blocked(a.BaseX, a.BaseY, x, y) { // somewhere people live, within range, not across a sealed border
					a.goTo(x, y, tripDaysMin+rng.IntN(tripDaysMax-tripDaysMin+1)) // for 1 to 7 days
				}
			}
		}
	})
}

// record saves today's counts and response stage for the CSV.
func (w *World) record() {
	w.History = append(w.History, w.counts())
	w.StageHist = append(w.StageHist, w.Stage)
	w.SealedHist = append(w.SealedHist, w.sealedCount())
	hospital, home, quarantined := w.isolationCounts()
	w.IsoHist = append(w.IsoHist, [3]int{hospital, home, quarantined})
}

// ---------- Plague among rats ----------

// updateRats spreads plague among rats within and between neighbouring areas. Dying rats release
// their fleas, which then look for a new host.
func (w *World) updateRats() {
	old := slices.Clone(w.Cells) // update every area from the same starting picture
	for cy := 0; cy < w.GridH; cy++ {
		for cx := 0; cx < w.GridW; cx++ {
			i := cy*w.GridW + cx // this area's place in the list
			o := old[i]
			if o.Rats == 0 && o.Infected == 0 && o.Fleas == 0 { // nothing here to change
				continue
			}
			pressure := ratInfectRate * o.Infected // infection from rats in this area
			if cx > 0 {
				pressure += ratSpreadRate * old[i-1].Infected // and a little from the area to the west
			}
			if cx < w.GridW-1 {
				pressure += ratSpreadRate * old[i+1].Infected // east
			}
			if cy > 0 {
				pressure += ratSpreadRate * old[i-w.GridW].Infected // north
			}
			if cy < w.GridH-1 {
				pressure += ratSpreadRate * old[i+w.GridW].Infected // south
			}
			newlyInfected := math.Min(o.Rats, pressure*o.Rats) // healthy rats catching it today
			died := ratDeathRate * o.Infected                  // infected rats dying today

			c := &w.Cells[i]
			c.Rats = o.Rats - newlyInfected
			c.Infected = o.Infected + newlyInfected - died
			c.Fleas = (o.Fleas + died*fleasPerRat) * (1 - fleaDecay) // fleas leave dead rats; some die off
			w.pestControl(c)                                         // pest control, if it has started here
		}
	}
}

// ---------- Catching plague from people and fleas ----------

// exposure is one person catching plague today, and which form.
type exposure struct {
	agent int   // who
	state State // ExposedBubonic or ExposedPneumonic
}

// sickGrid buckets agents into squares one infection radius wide, so each healthy agent only
// checks the sick agents in the 3 × 3 squares around it. Each square is a linked list:
// first[square] is one agent, next[agent] the next one in the same square, -1 ends the list.
type sickGrid struct {
	size        float64 // width of a square, km
	cols, rows  int     // squares across and down
	first       []int32 // sick agents
	next        []int32
	firstHealth []int32 // healthy agents, used to count crowds
	nextHealth  []int32
	crowd       []int32 // healthy agents within each sick agent's infection radius
}

// buildSickGrid sorts today's infectious and healthy agents into the squares.
func (w *World) buildSickGrid() *sickGrid {
	g := &w.sick
	if g.first == nil { // first day: make the lists
		g.size = math.Max(pneumonicRadius, ectoRadius) // a square is as wide as the furthest infection reaches
		g.cols = int(float64(w.Map.W)/g.size) + 1
		g.rows = int(float64(w.Map.H)/g.size) + 1
		g.first = make([]int32, g.cols*g.rows)
		g.next = make([]int32, len(w.Agents))
		g.firstHealth = make([]int32, g.cols*g.rows)
		g.nextHealth = make([]int32, len(w.Agents))
		g.crowd = make([]int32, len(w.Agents))
	}
	for i := range g.first {
		g.first[i], g.firstHealth[i] = -1, -1 // empty every square
	}
	for j, a := range w.Agents {
		s := int(a.Y/g.size)*g.cols + int(a.X/g.size) // the square they're in
		switch {
		case a.sick() && a.Hospital == 0: // infectious (hospital patients can't infect anyone): add to the front of the square's sick list
			g.next[j] = g.first[s]
			g.first[s] = int32(j)
		case a.State == Susceptible: // healthy: add to the square's healthy list
			g.nextHealth[j] = g.firstHealth[s]
			g.firstHealth[s] = int32(j)
		}
	}
	w.forBlocks(0, func(lo, hi int, _ *rand.Rand) { // the lists are built, so crowds can be counted on every core
		for j := lo; j < hi; j++ {
			if a := &w.Agents[j]; a.sick() && a.Hospital == 0 { // every infectious agent
				g.crowd[j] = int32(w.countHealthy(a, g)) // how many healthy people are near them
			}
		}
	})
	return g
}

// countHealthy counts the healthy agents within a sick agent's infection radius.
func (w *World) countHealthy(b *Agent, g *sickGrid) int {
	r := ectoRadius // bubonic spreads through fleas and lice
	if b.State == Pneumonic {
		r = pneumonicRadius // pneumonic spreads through the air
	}
	gx, gy := int(b.X/g.size), int(b.Y/g.size) // their square
	n := 0
	for sy := max(gy-1, 0); sy <= min(gy+1, g.rows-1); sy++ { // it and the squares around it
		for sx := max(gx-1, 0); sx <= min(gx+1, g.cols-1); sx++ {
			for i := g.firstHealth[sy*g.cols+sx]; i >= 0; i = g.nextHealth[i] { // every healthy agent there
				dx, dy := w.Agents[i].X-b.X, w.Agents[i].Y-b.Y
				if dx*dx+dy*dy < r*r { // close enough to catch it
					n++
				}
			}
		}
	}
	return n
}

// contactChance is one neighbour's daily chance of catching it from a sick agent. In a crowd the
// chance is scaled down so a sick agent infects about perDay people a day, however many are near;
// otherwise a dot in a big city would infect hundreds.
func (w *World) contactChance(chance, perDay float64, crowd int32) float64 {
	if perDay > 0 && crowd > 0 {
		chance = math.Min(chance, perDay/float64(crowd)) // shared out among the crowd
	}
	if w.Era.Modern && socialDistancing {
		chance *= 1 - distancing[w.Stage] // people mix less at this stage of the response
	}
	return chance
}

// newExposures finds everyone who catches plague today, as one list per block of dots. Nothing
// changes until every block has finished, so blocks can run on several cores at once.
func (w *World) newExposures() [][]exposure {
	g := w.buildSickGrid()
	caught := make([][]exposure, (len(w.Agents)+blockSize-1)/blockSize)
	w.forBlocks(stepExposure, func(lo, hi int, rng *rand.Rand) {
		b := lo / blockSize // this block's list; no other core touches it
		for i := lo; i < hi; i++ {
			if w.Agents[i].State != Susceptible { // only healthy people can catch it
				continue
			}
			if s, ok := w.catches(&w.Agents[i], g, rng); ok {
				caught[b] = append(caught[b], exposure{agent: i, state: s})
			}
		}
	})
	return caught
}

// catches tries all three routes for one healthy agent and returns the form caught, if any.
func (w *World) catches(a *Agent, g *sickGrid, rng *rand.Rand) (State, bool) {
	era := w.Era
	gx, gy := int(a.X/g.size), int(a.Y/g.size)                // their square
	for sy := max(gy-1, 0); sy <= min(gy+1, g.rows-1); sy++ { // it and the squares around it
		for sx := max(gx-1, 0); sx <= min(gx+1, g.cols-1); sx++ {
			for j := g.first[sy*g.cols+sx]; j >= 0; j = g.next[j] { // every infectious agent there
				b := &w.Agents[j]
				leak := 1.0
				if b.isolated() { // isolating at home: still sharing a home
					leak = homeLeak
				}
				dx, dy := a.X-b.X, a.Y-b.Y
				d2 := dx*dx + dy*dy // squared distance between them
				// route 1: breathing in pneumonic plague from someone close by
				if usePneumonic && b.State == Pneumonic && d2 < pneumonicRadius*pneumonicRadius &&
					rng.Float64() < leak*w.contactChance(era.PneumonicProb, era.PneumonicPerDay, g.crowd[j]) {
					return ExposedPneumonic, true
				}
				// route 2: human fleas or lice from someone with bubonic plague
				if useEcto && b.State == Bubonic && d2 < ectoRadius*ectoRadius &&
					rng.Float64() < leak*w.contactChance(era.EctoProb, era.EctoPerDay, g.crowd[j]) {
					return ExposedBubonic, true
				}
			}
		}
	}
	if useRats { // route 3: a bite from an infected rat flea
		cx, cy := w.cellOf(a.X, a.Y)
		cell := &w.Cells[cy*w.GridW+cx] // the rat area they're in
		bite := era.FleaBiteProb
		if cell.ControlDay >= 0 && w.Day >= int(cell.ControlDay) { // pest control is working here
			bite *= bitesInTreated
		}
		if rng.Float64() < cell.Fleas*bite { // more loose fleas, more bites
			return ExposedBubonic, true
		}
	}
	return Susceptible, false // stayed healthy today
}

// ---------- Counts and status text ----------

// counts returns how many dots are in each state.
func (w *World) counts() [numStates]int {
	var c [numStates]int
	for _, a := range w.Agents {
		c[a.State]++
	}
	return c
}

// Over reports whether nobody is incubating or sick and no plague is left in the rats.
func (w *World) Over() bool {
	c := w.counts()
	if c[ExposedBubonic]+c[ExposedPneumonic]+c[Bubonic]+c[Pneumonic] > 0 { // someone still has it
		return false
	}
	for _, cell := range w.Cells {
		if cell.Infected > 0.001 || cell.Fleas*w.Era.FleaBiteProb > 0.0001 { // rats or fleas could still infect someone
			return false
		}
	}
	return true
}

// PeoplePerDot is how many real people one dot stands for.
func (w *World) PeoplePerDot() float64 {
	return w.Map.TotalPeople / float64(len(w.Agents))
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

// Status returns the two lines of text under the map.
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

// thousands formats 98700 as "98,700".
func thousands(v float64) string {
	s := strconv.Itoa(int(math.Round(v)))
	for i := len(s) - 3; i > 0; i -= 3 { // every third digit from the right
		s = s[:i] + "," + s[i:]
	}
	return s
}

// uniform returns a random number between lo and hi.
func uniform(rng *rand.Rand, lo, hi float64) float64 {
	return lo + (hi-lo)*rng.Float64()
}

// ---------- Output ----------

// writeCSV saves one row per day, including an estimate of real deaths (dots × people per dot).
func writeCSV(w *World, name string) error {
	file, err := os.Create(name)
	if err != nil {
		return err
	}
	defer file.Close() // close the file when done

	out := csv.NewWriter(file)
	header := []string{"day", "susceptible", "exposed_bubonic", "exposed_pneumonic", "bubonic", "pneumonic", "recovered", "dead",
		"response_stage", "counties_sealed", "in_hospital", "isolating_at_home", "quarantined", "new_deaths", "estimated_real_deaths", "era", "began", "seed"}
	if err := out.Write(header); err != nil {
		return err
	}
	for day, c := range w.History { // one row per day
		row := []string{strconv.Itoa(day)}
		for _, n := range c { // the count for each state
			row = append(row, strconv.Itoa(n))
		}
		newDeaths := 0
		if day > 0 {
			newDeaths = c[Dead] - w.History[day-1][Dead] // deaths since yesterday
		}
		iso := w.IsoHist[day]
		row = append(row, stageNames[w.StageHist[day]], strconv.Itoa(w.SealedHist[day]),
			strconv.Itoa(iso[0]), strconv.Itoa(iso[1]), strconv.Itoa(iso[2]), strconv.Itoa(newDeaths),
			strconv.Itoa(int(math.Round(float64(c[Dead])*w.PeoplePerDot()))), w.Era.Year, w.StartName, strconv.FormatUint(w.Seed, 10))
		if err := out.Write(row); err != nil {
			return err
		}
	}
	out.Flush() // write anything still buffered
	return out.Error()
}

// runBatch runs outbreaks without a window and prints a line for each, then the average.
// Run r uses seed seed+r-1, so a batch can be repeated exactly.
func runBatch(m *Map, era *Era, runs int, seed uint64) {
	fmt.Printf("%s rules, %s dots (1 dot ≈ %s people), seeds %d–%d, %d cores\n\n", era.Year, thousands(numAgents), thousands(m.TotalPeople/numAgents), seed, seed+uint64(runs)-1, cores)
	fmt.Printf("%4s  %9s  %15s  %6s  %9s  %6s  %8s  %s\n", "run", "dead %", "≈ real deaths", "days", "national", "time", "seed", "began")
	batchStart := time.Now()
	var totalReal float64
	for r := 1; r <= runs; r++ {
		start := time.Now()
		w := newWorld(m, era, seed+uint64(r-1))
		for !w.Over() && w.Day < maxDays { // run until it dies out, or 10 years
			w.Step()
		}
		dead := w.History[w.Day][Dead]
		real := float64(dead) * w.PeoplePerDot() // dots to real people
		national := "-"                          // the day of the stay-at-home order, if there was one
		if w.Stage == National {
			national = "day " + strconv.Itoa(w.StageDay[National])
		}
		fmt.Printf("%4d  %8.3f%%  %15s  %6d  %9s  %5.1fs  %8d  %s\n", r, 100*float64(dead)/float64(len(w.Agents)),
			thousands(real), w.Day, national, time.Since(start).Seconds(), w.Seed, w.StartName)
		totalReal += real
	}
	fmt.Printf("\naverage: ≈ %s deaths   (%.1fs for all %d runs)\n\n", thousands(totalReal/float64(runs)), time.Since(batchStart).Seconds(), runs)
}

// randomSeed picks a seed when none is given. Seeds are kept short so they're easy to type.
func randomSeed() uint64 {
	return rand.Uint64N(1_000_000)
}

// ---------- Starting the program ----------

func main() {
	batch := flag.Int("batch", 0, "run this many outbreaks for each era without a window")             // -batch N
	seedFlag := flag.Int64("seed", -1, "the outbreak's seed (batch run r uses seed+r-1); -1 = random") // -seed N
	coresFlag := flag.Int("cores", runtime.NumCPU(), "how many cores to use (1 = no parallelism)")     // -cores N
	flag.Parse()

	setCores(*coresFlag) // every core this machine has, unless -cores says otherwise
	seed := randomSeed()
	if *seedFlag >= 0 {
		seed = uint64(*seedFlag) // replay a chosen outbreak
	}

	m, err := loadMap() // the map is built into the program
	if err != nil {
		log.Fatal(err)
	}
	if batchMode && *batch > 0 { // batch mode: no window
		for _, era := range eras {
			runBatch(m, era, *batch, seed)
		}
		return
	}

	fmt.Println("Seed", seed, "- run with -seed", seed, "to see this outbreak again")
	w := runLive(m, seed) // open the window; returns when it is closed
	if saveCSV {
		if err := writeCSV(w, csvFile); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Saved daily counts to", csvFile)
	}
}
