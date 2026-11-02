// The 2026 public-health response, based on how Ireland handled its 2020 public-health emergency.
// The README lists the sources. Values marked "assumption" are estimates. One response covers the
// whole island, although Northern Ireland has its own health service.
package main

import (
	"cmp"          // comparing distances when sorting contacts
	"math"         // rounding and distances
	"math/rand/v2" // the chance parts: who seeks care, who is diagnosed, who is traced
	"slices"       // sorting contacts
)

// ---------- How far the response has got ----------

// Stage is how far the official response has gone.
type Stage int

const (
	Undetected Stage = iota // nobody has been diagnosed yet
	FirstCase               // the first case is confirmed: diagnosis and tracing begin
	Emergency               // public health emergency: people mix less and travel less
	National                // stay-at-home order: the strictest stage
	numStages               // how many stages there are, for sizing arrays
)

// stageNames are shown in the status line and the CSV.
var stageNames = [numStages]string{"Not yet detected", "First case confirmed", "Public health emergency", "Stay-at-home order"}

// stageStartsAfter is how many days after the first confirmed case each stage begins. In Ireland
// in 2020 schools closed 12 days after the first case and a stay-at-home order followed 15 days later.
var stageStartsAfter = [numStages]int{0, 0, 12, 27}

// ---------- Getting diagnosed ----------

// diagnoseChance is the daily chance that a doctor recognises plague in someone who has come for
// care. It starts low because Irish doctors have not seen plague in modern times, and rises as
// the outbreak becomes known. Suspected cases must be notified at once (HPSC).
var diagnoseChance = [numStages]float64{0.02, 0.08, 0.15, 0.3} // assumption

// How many days (shortest, longest) after falling ill people go to a doctor.
var (
	seekBubonic   = [2]int{2, 6} // bubonic plague starts like flu, so people wait
	seekPneumonic = [2]int{1, 3} // pneumonic symptoms are dramatic, so people go sooner (assumption)
	costDelay     = [2]int{1, 5} // extra days for people put off by the cost (assumption)
)

// ---------- Treatment, measures and pest control ----------

const (
	// 25% in the Republic delay GP visits because of cost (Pfizer Health & Science Index 2023);
	// GP care is free in Northern Ireland. Weighted by population.
	costBarrier  = 0.19
	neverSeek    = 0.15 // share of sick people who never see a doctor (assumption)
	alertSpeedUp = 0.7  // once an emergency is declared, delays shrink to 70% (assumption)

	treatedBubonicDie   = 0.13 // death rate with antibiotics (Clinical Infectious Diseases review)
	treatedPneumonicDie = 0.31 // same review, pneumonic plague

	traceRadius     = 2 // close contacts are within twice the pneumonic infection distance
	contactsPerCase = 5 // close contacts traced per case; one dot is ~35 people, so 5 dots is 5 contacts each (assumption)

	lockdownAt = 0.005 // a county seals when this share of the people in it are ill (0.5%), and reopens below half of it

	// Rats are the owner's or council's job in Ireland; the HSE treats homes only in Dublin.
	controlKm      = 30   // pest control covers this many km around each diagnosed case (assumption)
	controlDelay   = 10   // days before pest control starts working (assumption)
	fleaKill       = 0.25 // share of infected fleas killed each day once control starts
	ratKill        = 0.15 // share of infected rats killed each day once rat control starts
	fleasFirst     = 7    // rats are killed only after a week of flea control, or their fleas move to people (CDC)
	bitesInTreated = 0.7  // flea bites drop to 70% where pest control is working
)

var (
	// Share of incubating close contacts reached with antibiotics before they fall ill. The HSE
	// reached 94% of contacts in 2020, usually within a day, but only those treated in time count.
	traceShare = [numStages]float64{0, 0.3, 0.4, 0.5} // assumption

	// How much less people mix at each stage: contacts fell 74% under the 2020 UK lockdown (CoMix).
	distancing = [numStages]float64{0, 0, 0.35, 0.75}

	// Share of normal car and train trips still made: in 2020 car traffic fell about 35% after
	// schools closed and about 70% under the stay-at-home order (CSO/TII).
	tripsShare = [numStages]float64{1, 1, 0.65, 0.3}
)

// ---------- Declaring emergencies ----------

// updateStage moves the response on once enough days have passed since the first case.
func (w *World) updateStage() {
	if w.FirstCaseDay < 0 { // nobody diagnosed yet, so there is no response
		return
	}
	for s := Stage(0); s < numStages; s++ { // check every stage in order
		if w.Day-w.FirstCaseDay >= stageStartsAfter[s] && s > w.Stage { // its day has come and it's new
			w.Stage = s           // move up to that stage
			w.StageDay[s] = w.Day // remember when, for the status line
		}
	}
	if w.Stage == National { // under the stay-at-home order, rat surveillance finds outbreaks with no human case
		for i := range w.Cells { // look at every rat area
			if w.Cells[i].Infected > 0.001 && w.Cells[i].ControlDay < 0 { // plague in its rats, no pest control yet
				w.Cells[i].ControlDay = int32(w.Day + controlDelay) // schedule pest control there
			}
		}
	}
}

// ---------- Seeking care and diagnosis ----------

// seekCare decides which day a newly ill person goes to a doctor. With pneumonic plague, waiting
// a day or two is usually fatal.
func (w *World) seekCare(a *Agent) {
	if rand.Float64() < neverSeek { // some people never go
		a.SeekDay = 1 << 30 // a day that never comes
		return
	}
	d := seekBubonic          // usual delay for bubonic plague
	if a.State == Pneumonic { // pneumonic plague
		d = seekPneumonic // feels worse, so they go sooner
	}
	days := float64(d[0] + rand.IntN(d[1]-d[0]+1)) // a random delay between the shortest and longest
	if rand.Float64() < costBarrier {              // put off by the cost of a GP visit
		days += float64(costDelay[0] + rand.IntN(costDelay[1]-costDelay[0]+1)) // so they wait longer
	}
	if w.Stage >= Emergency { // the outbreak is in the news
		days *= alertSpeedUp // so people go sooner
	}
	a.SeekDay = w.Day + int(math.Round(days)) // the day they will see a doctor
}

// diagnose treats sick people who have seen a doctor and been recognised, traces contacts of
// pneumonic cases, and starts pest control around each case. The first diagnosis starts the
// official response.
func (w *World) diagnose() {
	var newPneumonic []*Agent // today's diagnosed pneumonic cases, whose contacts will be traced
	for i := range w.Agents { // look at everyone
		a := &w.Agents[i]
		if !a.sick() || a.Treated || w.Day < a.SeekDay { // not ill, already treated, or not at a doctor yet
			continue
		}
		if rand.Float64() >= diagnoseChance[w.Stage] && !a.quarantined(w.Day) { // the doctor doesn't recognise plague today (quarantined contacts are checked for it daily)
			continue
		}
		if w.FirstCaseDay < 0 { // this is the first case ever confirmed
			w.FirstCaseDay = w.Day // the response starts counting from today
			w.updateStage()        // and moves to "First case confirmed" at once
		}
		a.Treated = true // antibiotics from now on
		if isolateDiagnosed {
			w.admit(a) // to an isolation hospital, or isolated at home if every bed is taken
		}
		w.Diagnosed++                                // count for the status line
		w.LastCase[w.Map.CountyAt(a.X, a.Y)] = w.Day // the county they're in has a new case
		w.LastIslandCase = w.Day
		if a.State == Pneumonic {
			newPneumonic = append(newPneumonic, a) // their close contacts need tracing
		}
		w.startPestControl(a.X, a.Y) // send pest control to where they are
	}
	if contactTracing { // switched on in main.go
		w.traceContacts(newPneumonic) // give today's cases' close contacts antibiotics
	}
}

// ---------- Contact tracing ----------

// traceContacts reaches some close contacts of each new pneumonic case: up to contactsPerCase of
// them, the people they infected first (those are who tracing is meant to find), then whoever was
// nearest. Each contact gets 7 days of preventive antibiotics, so anyone incubating pneumonic
// plague never falls ill, and is quarantined at home for 7 days with a daily check.
func (w *World) traceContacts(cases []*Agent) {
	r := traceRadius * pneumonicRadius // how close counts as a close contact, in km
	for _, c := range cases {
		var near []int // everyone close to this case who can still be traced
		for i := range w.Agents {
			a := &w.Agents[i]
			if a.State == Dead || a.Treated || a.quarantined(w.Day) || a == c { // dead, isolated, already in quarantine, or the case
				continue
			}
			if math.Hypot(a.X-c.X, a.Y-c.Y) < r {
				near = append(near, i)
			}
		}
		slices.SortFunc(near, func(i, j int) int { // the people they infected first, then the nearest
			a, b := &w.Agents[i], &w.Agents[j]
			if ea, eb := a.State == ExposedPneumonic, b.State == ExposedPneumonic; ea != eb {
				if ea {
					return -1
				}
				return 1
			}
			return cmp.Compare(math.Hypot(a.X-c.X, a.Y-c.Y), math.Hypot(b.X-c.X, b.Y-c.Y))
		})
		for _, i := range near[:min(len(near), contactsPerCase)] {
			a := &w.Agents[i]
			if rand.Float64() >= traceShare[w.Stage] { // not reached in time
				continue
			}
			if a.State == ExposedPneumonic {
				a.Become(Susceptible) // antibiotics stop the infection
				w.Prevented++         // count for the status line
			}
			w.quarantine(a) // home for 7 days
		}
	}
}

// ---------- Pest control ----------

// startPestControl schedules pest control in every rat area within controlKm of a case.
func (w *World) startPestControl(x, y float64) {
	reach := controlKm / ratCell                    // the radius in rat areas instead of km
	cx, cy := w.cellOf(x, y)                        // the rat area the case is in
	for dy := -int(reach); dy <= int(reach); dy++ { // rows of areas around it
		for dx := -int(reach); dx <= int(reach); dx++ { // columns of areas around it
			nx, ny := cx+dx, cy+dy                                                                        // the area being checked
			if nx < 0 || ny < 0 || nx >= w.GridW || ny >= w.GridH || float64(dx*dx+dy*dy) > reach*reach { // off the map or outside the circle
				continue
			}
			if c := &w.Cells[ny*w.GridW+nx]; c.ControlDay < 0 { // not already scheduled
				c.ControlDay = int32(w.Day + controlDelay) // starts after the delay
			}
		}
	}
}

// pestControl kills fleas, then rats, in an area once its pest control has started.
func (w *World) pestControl(c *Cell) {
	if c.ControlDay < 0 || w.Day < int(c.ControlDay) { // not scheduled, or not started yet
		return
	}
	c.Fleas *= 1 - fleaKill                    // flea control starts at once
	if w.Day-int(c.ControlDay) >= fleasFirst { // after a week of flea control
		c.Infected *= 1 - ratKill // rat control starts too
	}
}

// ---------- County lockdowns ----------

// updateLockdowns seals and reopens counties with the same rule as the towns in Team-Dolphin-Dog:
// a county seals when lockdownAt of the people in it are ill, and reopens when that falls below
// half. A county that was clicked stays the way it was set. 1348 has no lockdowns of its own.
func (w *World) updateLockdowns() {
	if !countyLockdowns || !w.Era.Modern {
		return
	}
	ill, people := make([]int, len(w.Sealed)), make([]int, len(w.Sealed)) // per county, by where people are today
	for i := range w.Agents {
		a := &w.Agents[i]
		if a.State == Dead {
			continue
		}
		c := w.Map.CountyAt(a.X, a.Y)
		people[c]++
		if a.State == Bubonic || a.State == Pneumonic {
			ill[c]++
		}
	}
	newlySealed := make([]bool, len(w.Sealed))
	anyNew := false
	for c := 1; c < len(w.Sealed); c++ { // every county (0 is the sea)
		if w.Manual[c] || people[c] == 0 { // clicked counties are left alone
			continue
		}
		share := float64(ill[c]) / float64(people[c])
		if !w.Sealed[c] && share >= lockdownAt { // closing today
			w.Sealed[c] = true
			newlySealed[c] = true
			anyNew = true
		} else if w.Sealed[c] && share < lockdownAt/2 { // few enough ill: open again
			w.Sealed[c] = false
		}
	}
	if anyNew {
		w.sendHome(newlySealed)
	}
}

// toggleSeal seals or opens county c by hand (a click on the map). After that the rule leaves it alone.
func (w *World) toggleSeal(c int) {
	w.Sealed[c] = !w.Sealed[c]
	w.Manual[c] = true
	if w.Sealed[c] {
		closing := make([]bool, len(w.Sealed))
		closing[c] = true
		w.sendHome(closing)
	}
}

// sendHome puts everyone on the right side of the borders that are closing today.
func (w *World) sendHome(closing []bool) {
	for i := range w.Agents {
		a := &w.Agents[i]
		if a.State == Dead || a.Hospital > 0 { // the dead and hospital patients stay where they are; diagnosed people at home stay isolated there
			continue
		}
		if a.AwayDays > 0 && (closing[w.Map.CountyAt(a.HomeX, a.HomeY)] || closing[w.Map.CountyAt(a.BaseX, a.BaseY)]) { // on a trip from, or to, a county that's closing
			a.goTo(a.HomeX, a.HomeY, 0) // home today
			continue
		}
		here, base := w.Map.CountyAt(a.X, a.Y), w.Map.CountyAt(a.BaseX, a.BaseY)
		if here != base && (closing[here] || closing[base]) { // out for the day across a border that's closing
			a.X, a.Y = a.BaseX, a.BaseY // back to their own side
		}
	}
}

// blocked reports whether going from (x0, y0) to (x1, y1) crosses a sealed county border.
func (w *World) blocked(x0, y0, x1, y1 float64) bool {
	c0, c1 := w.Map.CountyAt(x0, y0), w.Map.CountyAt(x1, y1)
	return c0 != c1 && (w.Sealed[c0] || w.Sealed[c1]) // a different county, and either side is sealed
}

// sealedCount is how many counties are sealed today.
func (w *World) sealedCount() int {
	n := 0
	for _, sealed := range w.Sealed {
		if sealed {
			n++
		}
	}
	return n
}

// ---------- Travel cuts ----------

// travelShare is the share of normal trips still made at the current stage.
func (w *World) travelShare() float64 {
	if !w.Era.Modern || !travelCuts { // 1348, or travel cuts switched off
		return 1 // everyone travels as normal
	}
	return tripsShare[w.Stage]
}

// ---------- Treatment ----------

// deathChance is the chance a sick person dies when their illness ends.
func deathChance(a *Agent) float64 {
	switch {
	case a.State == Bubonic && a.Treated:
		return treatedBubonicDie // bubonic, with antibiotics
	case a.State == Pneumonic && a.Treated && a.Hospital == 0:
		return treatedPneumonicDie * homePneumonic // pneumonic, with antibiotics but no hospital bed
	case a.State == Pneumonic && a.Treated:
		return treatedPneumonicDie // pneumonic, with antibiotics in hospital
	case a.State == Pneumonic:
		return pneumonicDeathProb // pneumonic, untreated: almost always fatal
	}
	return bubonicDeathProb // bubonic, untreated
}
