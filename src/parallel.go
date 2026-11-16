// Running the slowest parts of each day on several cores, and the seeded random numbers that keep
// an outbreak exactly the same however many cores are used.
package main

import (
	"math/rand/v2" // the seeded random-number generators
	"runtime"      // how many cores to use
	"sync"         // waiting for every core to finish (the "join")
	"sync/atomic"  // handing out blocks without a lock
)

// ---------- Blocks and their random numbers ----------

// The dots are split into fixed blocks. Each block draws its random numbers from its own
// generator, made from (seed, day, step, block), so a dot's random numbers never depend on which
// core handles it or in what order. That's why the same seed gives the same outbreak on any
// number of cores.
const blockSize = 4096 // dots per block

// The steps of a day (and of setting up). Each gets different random numbers.
const (
	stepSetup    = iota + 1 // placing everyone's home
	stepStart               // choosing where the outbreak begins
	stepTravel              // daily movement and trips
	stepExposure            // who catches plague today
	stepProgress            // illnesses moving on, and today's infections taking hold
	stepResponse            // diagnosis and contact tracing (on one core)
)

// newStream makes the generator for one seed, day, step and block.
func newStream(seed uint64, day, step, block int) *rand.Rand {
	return rand.New(rand.NewPCG(seed, uint64(day)<<40|uint64(step)<<32|uint64(block)))
}

// ---------- Cores ----------

// cores is how many cores the slow steps are shared between. -cores sets it; it starts as every
// core this machine has, so it adapts to whatever Mac or PC it runs on.
var cores = runtime.NumCPU()

// setCores uses n cores from now on (at least one).
func setCores(n int) {
	cores = max(n, 1)
	runtime.GOMAXPROCS(cores) // Go itself uses no more than that many cores either
}

// blocks is how many blocks the dots are split into.
func (w *World) blocks() int {
	return (len(w.Agents) + blockSize - 1) / blockSize
}

// forBlocks runs fn on every block of dots (lo … hi-1), with that block's generator for today
// and this step, shared out between the cores: each core keeps taking the next block until none
// are left (fork-join). With one core it's a plain loop, in order.
func (w *World) forBlocks(step int, fn func(lo, hi int, rng *rand.Rand)) {
	blocks := w.blocks()
	work := func(b int) {
		lo, hi := b*blockSize, min((b+1)*blockSize, len(w.Agents))
		fn(lo, hi, newStream(w.Seed, w.Day, step, b))
	}
	if cores == 1 { // one core: no goroutines at all
		for b := 0; b < blocks; b++ {
			work(b)
		}
		return
	}
	var next atomic.Int64 // the next block not yet handed out
	var wg sync.WaitGroup
	for range min(cores, blocks) { // fork: one goroutine per core
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := int(next.Add(1)) - 1; b < blocks; b = int(next.Add(1)) - 1 { // claim the next block
				work(b)
			}
		}()
	}
	wg.Wait() // join: every block is done
}
