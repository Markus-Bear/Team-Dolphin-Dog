// Quarantine in 2026: isolation hospitals with a limited number of beds, isolation at home when
// they are full, and 7 days of quarantine for traced close contacts. The hospitals work like the
// ones in our Team-Dolphin-Dog simulation: one per county, a patient there can't infect anyone,
// and a patient who doesn't get a bed is more likely to die. The README lists the sources.
package main

import "math"

const (
	// Public acute inpatient beds: about 11,000 in the Republic (3.9 million bed days in 2023,
	// ESRI 2025) and 5,835 in Northern Ireland (NISRA, 2024/25), per 1,000 people.
	bedsPer1000Republic = 2.1
	bedsPer1000North    = 3.0
	plagueBedShare      = 0.2 // share of beds freed for plague by cancelling planned care, as in March 2020 (assumption)

	homeLeak       = 0.25 // someone isolating at home infects a quarter as many people: they still share a home (assumption)
	homePneumonic  = 1.5  // pneumonic plague needs hospital care; at home it kills 1.5 times as often (assumption)
	quarantineDays = 7    // close contacts are quarantined and checked daily for 7 days (plague's longest incubation)
)

// northernCounties are the six counties of Northern Ireland, which has more beds per person.
var northernCounties = map[string]bool{"Antrim": true, "Armagh": true, "Down": true, "Fermanagh": true, "Londonderry": true, "Tyrone": true}

// Hospital is one county's isolation hospital.
type Hospital struct {
	X, Y     float64 // where it is: the busiest km of the county, where its main hospital would be
	Beds     int     // beds for plague patients, in dots
	Patients int     // beds in use, in dots
}

// hospitals puts an isolation hospital in every county, with beds for its population.
// hospitals[c] is county c's; hospitals[0], the sea, has no beds.
func (m *Map) hospitals(peoplePerDot float64) []Hospital {
	hs := make([]Hospital, len(m.Counties))
	people := make([]float64, len(m.Counties))
	busiest := make([]float32, len(m.Counties))
	for i, c := range m.county { // every km of the map
		if c == 0 || !m.land[i] {
			continue
		}
		people[c] += float64(m.density[i])
		if m.density[i] > busiest[c] { // the most people per km² so far in this county
			busiest[c] = m.density[i]
			hs[c].X, hs[c].Y = float64(i%m.W)+0.5, float64(i/m.W)+0.5 // the middle of that km
		}
	}
	for c := 1; c < len(hs); c++ {
		rate := bedsPer1000Republic
		if northernCounties[m.Counties[c].Name] {
			rate = bedsPer1000North
		}
		beds := people[c] / 1000 * rate * plagueBedShare        // real beds for plague
		hs[c].Beds = max(int(math.Round(beds/peoplePerDot)), 1) // in dots, at least one
	}
	return hs
}

// admit takes a newly diagnosed person to the isolation hospital in their county, or to the
// nearest one with a free bed. If every bed on the island is taken, they isolate at home.
func (w *World) admit(a *Agent) {
	best, bestDist := 0, math.Inf(1)
	for c := 1; c < len(w.Hospitals); c++ {
		h := &w.Hospitals[c]
		if h.Patients >= h.Beds { // full
			continue
		}
		d := math.Hypot(h.X-a.X, h.Y-a.Y)
		if c == w.Map.CountyAt(a.X, a.Y) {
			d = -1 // their own county's hospital comes first
		}
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	if best == 0 { // no free bed anywhere: isolate at home
		a.X, a.Y = a.HomeX, a.HomeY
		return
	}
	w.Hospitals[best].Patients++
	a.Hospital = best
	a.X, a.Y = w.Hospitals[best].X, w.Hospitals[best].Y // an ambulance takes them; medical transfers may cross sealed borders
}

// discharge frees a patient's bed when their illness ends. Survivors go home; the dead stay where they died.
// It runs on several cores at once (from progress), so the bed count is changed under a lock.
func (w *World) discharge(a *Agent) {
	if a.Hospital == 0 {
		return
	}
	w.bedLock.Lock()
	w.Hospitals[a.Hospital].Patients-- // one bed fewer in use; the order doesn't matter
	w.bedLock.Unlock()
	a.Hospital = 0
	if a.State != Dead {
		a.goTo(a.HomeX, a.HomeY, 0)
	}
}

// quarantine keeps a traced close contact at home for quarantineDays, checked by a doctor every day.
func (w *World) quarantine(a *Agent) {
	if !quarantineContacts {
		return
	}
	a.QuarantineEnd = w.Day + quarantineDays
	a.goTo(a.HomeX, a.HomeY, 0) // straight home, even from a trip
}

// isolationCounts returns how many dots are in hospital, isolating at home, and in quarantine.
func (w *World) isolationCounts() (hospital, home, quarantined int) {
	for i := range w.Agents {
		a := &w.Agents[i]
		switch {
		case a.Hospital > 0:
			hospital++
		case a.isolated() && a.sick():
			home++
		case a.quarantined(w.Day) && a.State != Dead:
			quarantined++
		}
	}
	return hospital, home, quarantined
}

// totalBeds is the number of plague beds on the island, in dots.
func (w *World) totalBeds() int {
	n := 0
	for _, h := range w.Hospitals {
		n += h.Beds
	}
	return n
}
