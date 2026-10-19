package main // same package as live.go, so they can use each other's code

import (
	"fmt"          // fmt.Sprintf: build text like "Susceptible=315"
	"image/color"  // colour types, used for each agent's colour
	"math/rand/v2" // random numbers
)

const (
	numAgents        = 1500 // how many agents to create
	stepSize         = 5.0  // the furthest an agent can move per step, in each direction
	initialInfected  = 10   // how many agents start out infectious
	infectRadius     = 10.0 // how close an agent must be to catch it
	transmissionProb = 0.05 // chance of catching it from each infectious neighbour, per step
	infectiousSteps  = 50   // how many steps an agent stays infectious
	incubationSteps  = 10   // how many steps an agent is infected before it becomes infectious
)

var (
	xBounds = [2]float64{0, 500} // the world's left and right edges
	yBounds = [2]float64{0, 500} // the world's bottom and top edges
)

// State is an agent's health status. It's stored as a small number (0, 1, 2, …).
type State uint8

const (
	Susceptible State = iota // 0: healthy, can catch the disease
	Exposed                  // 1: infected, but can't pass it on yet
	Infectious               // 2: sick, can pass it on
	Recovered                // 3: immune, can't catch it again
	numStates                // 4: keep last – it equals how many states there are
)

var stateNames = [numStates]string{"Susceptible", "Exposed", "Infectious", "Recovered"} // label for each state
var stateColours = [numStates]color.Color{Green, Orange, Red, Blue}                     // colour for each state

// stateDuration is how many steps each state lasts before moving on to the next one.
// 0 means it lasts until something else changes it.
var stateDuration = [numStates]int{0, incubationSteps, infectiousSteps, 0} // S: until infected; E: 10; I: 50; R: forever

// Agent is one person in the simulation.
type Agent struct {
	X, Y  float64 // where the agent is
	State State   // the agent's health status (starts as 0 = Susceptible)
	Timer int     // steps left in the current state
}

// Become switches the agent into a new state and starts that state's timer.
func (a *Agent) Become(s State) {
	a.State = s                // set the new state
	a.Timer = stateDuration[s] // start the countdown for how long it lasts
}

// Progress counts the timer down and moves the agent on to the next state when it runs out.
func (a *Agent) Progress() {
	if stateDuration[a.State] == 0 { // this state has no time limit…
		return // …so there's nothing to count down
	}
	a.Timer--         // one step less left in this state
	if a.Timer <= 0 { // time's up…
		a.Become(a.State + 1) // …move to the next state in the list (Infectious + 1 = Recovered)
	}
}

// uniform returns a random number between lo and hi.
func uniform(lo, hi float64) float64 {
	return lo + (hi-lo)*rand.Float64() // rand.Float64() is between 0 and 1; stretch and shift it into [lo, hi)
}

// newAgents places n agents at random positions and makes the first few infectious.
func newAgents(n int) []Agent {
	agents := make([]Agent, n) // a list of n agents
	for i := range agents {    // for each agent
		agents[i] = Agent{
			X: uniform(xBounds[0], xBounds[1]), // random x inside the world
			Y: uniform(yBounds[0], yBounds[1]), // random y inside the world
		} // State isn't set, so it starts as 0 = Susceptible
	}
	for i := 0; i < initialInfected; i++ { // the first 10 agents…
		agents[i].Become(Infectious) // …start out infectious
	}
	return agents // hand the list back
}

// Move takes a random step, bouncing off the walls.
func (a *Agent) Move() { // (a *Agent) makes this a method: agent.Move(). The * lets it change the real agent, not a copy
	a.X = reflect(a.X+stepSize*uniform(-1, 1), xBounds) // move x by between -5 and +5, then bounce off a wall if needed
	a.Y = reflect(a.Y+stepSize*uniform(-1, 1), yBounds) // same for y
}

// reflect mirrors a coordinate that has stepped past a wall back inside the bounds.
func reflect(v float64, bounds [2]float64) float64 {
	for v < bounds[0] || v > bounds[1] { // keep going while v is outside the world
		if v < bounds[0] { // past the low wall (e.g. -3 with a wall at 0)…
			v = 2*bounds[0] - v // …mirror it back inside (-3 → 3)
		} else { // past the high wall (e.g. 503 with a wall at 500)…
			v = 2*bounds[1] - v // …mirror it back inside (503 → 497)
		}
	}
	return v // v is now inside the world
}

// newInfections decides who catches the disease this step, using only the current states.
// Each infectious neighbour within infectRadius gives one transmissionProb roll.
func newInfections(agents []Agent) []int {
	var caught []int        // indexes of agents who catch it this step (starts empty)
	for i := range agents { // for every agent i…
		if agents[i].State != Susceptible { // only healthy agents can catch it…
			continue // …so skip everyone else
		}
		for j := range agents { // look at every other agent j
			if agents[j].State != Infectious { // only infectious agents can pass it on…
				continue // …so skip everyone else
			}
			dx, dy := agents[i].X-agents[j].X, agents[i].Y-agents[j].Y // gap between i and j, sideways and up/down
			// close enough? (distance² < radius², which avoids a square root) AND the random roll succeeds?
			if dx*dx+dy*dy < infectRadius*infectRadius && rand.Float64() < transmissionProb {
				caught = append(caught, i) // remember that i caught it
				break                      // i is already infected, so stop checking other neighbours
			}
		}
	}
	return caught // the list of newly infected agents
}

// step advances the simulation by one tick. Infections are worked out first and
// applied together afterwards, so the order of the agents doesn't matter.
func step(agents []Agent) {
	for i := range agents { // for every agent
		agents[i].Move() // move it
	}
	caught := newInfections(agents) // decide who catches it, without changing anyone yet
	for i := range agents {         // for every agent
		agents[i].Progress() // count down its timer, and move it on if time's up
	}
	for _, i := range caught { // for each agent who caught it this step…
		agents[i].Become(Exposed) // newly caught agents are Exposed first; Progress turns them Infectious after 10 steps
	}
}

// snapshot records every agent's position and colour, plus a count of each state for the legend.
func snapshot(agents []Agent) Frame {
	f := Frame{
		X:      make([]float64, len(agents)),     // room for every agent's x
		Y:      make([]float64, len(agents)),     // room for every agent's y
		Colour: make([]color.Color, len(agents)), // room for every agent's colour
	}
	var counts [numStates]int  // one counter per state, all starting at 0
	for i, a := range agents { // for every agent
		f.X[i], f.Y[i] = a.X, a.Y           // copy its position
		f.Colour[i] = stateColours[a.State] // look up the colour for its state
		counts[a.State]++                   // add 1 to that state's count
	}
	for s := range numStates { // s = 0, 1, … for each state
		label := fmt.Sprintf("%s=%d", stateNames[s], counts[s])                        // e.g. "Infectious=42"
		f.Legend = append(f.Legend, LegendItem{Label: label, Colour: stateColours[s]}) // add it to the legend
	}
	return f // the finished frame
}

func main() { // the program starts here
	runLive() // open the window and run the simulation (in live.go)
}
