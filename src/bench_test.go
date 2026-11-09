// A test that using several cores never changes an outbreak, and a benchmark of one simulated day
// at different numbers of cores. They work the same on a Mac and on Windows:
//
//	go test ./src -run SameResult                   same outbreak on 1, 2 and all cores
//	go test ./src -run XXX -bench Day -benchtime 3x  time per day at 1, 2, 4 … all cores
package main

import (
	"encoding/binary" // numbers to bytes for the fingerprint
	"fmt"             // benchmark names
	"hash/fnv"        // the fingerprint of an outbreak
	"math"            // float bits for the fingerprint
	"runtime"         // how many cores this machine has
	"sync"            // loading the map once
	"testing"
)

// testMap loads the map once for the test and the benchmark.
var testMap = sync.OnceValue(func() *Map {
	m, err := loadMap()
	if err != nil {
		panic(err)
	}
	return m
})

// simulateDays sets up an outbreak and runs it for some days on the given number of cores.
func simulateDays(era *Era, seed uint64, days, n int) *World {
	setCores(n)
	w := newWorld(testMap(), era, seed)
	for d := 0; d < days && !w.Over(); d++ {
		w.Step()
	}
	return w
}

// fingerprint sums up an outbreak in one number: the daily counts, every dot, every rat area and
// every hospital. Two runs with the same fingerprint simulated the same outbreak.
func fingerprint(w *World) uint64 {
	h := fnv.New64a()
	put := func(v uint64) { h.Write(binary.LittleEndian.AppendUint64(nil, v)) }
	putF := func(f float64) { put(math.Float64bits(f)) }
	for _, day := range w.History {
		for _, n := range day {
			put(uint64(n))
		}
	}
	for _, a := range w.Agents {
		putF(a.X)
		putF(a.Y)
		putF(a.HomeX)
		putF(a.HomeY)
		put(uint64(a.State)<<48 | uint64(a.Timer)<<32 | uint64(uint32(a.SeekDay)))
		put(uint64(a.AwayDays)<<32 | uint64(uint32(a.QuarantineEnd)))
		put(uint64(a.Hospital))
		if a.Treated {
			put(1)
		}
	}
	for _, c := range w.Cells {
		putF(c.Rats)
		putF(c.Infected)
		putF(c.Fleas)
		put(uint64(uint32(c.ControlDay)))
	}
	for _, hs := range w.Hospitals {
		put(uint64(hs.Patients))
	}
	for _, sealed := range w.Sealed {
		if sealed {
			put(1)
		}
		put(0)
	}
	put(uint64(w.Diagnosed))
	put(uint64(w.Prevented))
	return h.Sum64()
}

// TestSameResult checks that 1 core, 2 cores and every core give exactly the same outbreak.
func TestSameResult(t *testing.T) {
	defer setCores(runtime.NumCPU())
	for _, era := range eras {
		for _, seed := range []uint64{1, 2} {
			want := fingerprint(simulateDays(era, seed, 40, 1))
			for _, n := range []int{2, runtime.NumCPU()} {
				if got := fingerprint(simulateDays(era, seed, 40, n)); got != want {
					t.Errorf("%s seed %d: %d cores gave a different outbreak from 1 core", era.Year, seed, n)
				}
			}
		}
	}
}

// coreCounts are 1, 2, 4, 8 … up to this machine's number of cores, and that number itself.
func coreCounts() []int {
	var list []int
	for n := 1; n < runtime.NumCPU(); n *= 2 {
		list = append(list, n)
	}
	return append(list, runtime.NumCPU())
}

// BenchmarkDay times days 40 to 60 of three 2026 outbreaks (near their peak) at each core count.
// Getting each outbreak to day 40 isn't timed.
func BenchmarkDay(b *testing.B) {
	const fromDay, days = 40, 20
	seeds := []uint64{11, 12, 13} // three outbreaks that start in different places and are still going on day 60
	defer setCores(runtime.NumCPU())
	for _, n := range coreCounts() {
		b.Run(fmt.Sprintf("cores=%d", n), func(b *testing.B) {
			for b.Loop() {
				for _, seed := range seeds {
					b.StopTimer()
					w := simulateDays(&Era2026, seed, fromDay, n) // the same starting point every time
					b.StartTimer()
					for d := 0; d < days; d++ {
						w.Step()
					}
				}
			}
			b.ReportMetric(float64(b.Elapsed().Microseconds())/1000/float64(b.N*len(seeds)*days), "ms/day")
		})
	}
}
