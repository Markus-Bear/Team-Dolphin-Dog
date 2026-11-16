// The benchmark: how long a simulated day takes at different numbers of dots and cores. Every
// timed run is added as one row to bench/results.csv, so results from every machine end up in
// one file. report.go draws the graphs from that file.
//
//	go run ./src -bench                  the default dots and cores
//	go run ./src -bench -agents 10000,200000 -cores 1,2,4 -repeats 3 -days 20
package main

import (
	"encoding/csv"  // writing results.csv
	"fmt"           // progress on the terminal
	"os"            // the results file and the hostname
	"os/exec"       // asking git for the commit and the system for the CPU's name
	"path/filepath" // bench/results.csv on any system
	"runtime"       // how many cores this machine has
	"slices"        // sorting the list of cores
	"strconv"       // numbers to and from text
	"strings"       // splitting "1,2,4" and tidying command output
	"time"          // the stopwatch
)

const (
	benchDir  = "bench"       // where the results and graphs go
	benchFile = "results.csv" // one row per timed run
	benchSeed = 11            // repeat r uses seed 11 + r - 1: outbreaks still going on day 60 (as in BenchmarkDay)
)

// benchAgents are the numbers of dots timed when -agents isn't given.
var benchAgents = []int{10_000, 50_000, 200_000, 1_000_000}

// benchHeader is the first line of results.csv.
var benchHeader = []string{"date", "machine", "cpu", "logical_cores", "commit", "era", "agents", "cores", "repeat", "seed", "from_day", "days", "ms_per_day", "dead"}

// benchOptions is everything -bench was asked to do.
type benchOptions struct {
	agents  []int  // numbers of dots to time
	cores   []int  // numbers of cores to time
	repeats int    // timed runs for each number of dots and cores
	days    int    // days timed in each run
	fromDay int    // the day the timing starts; the days before it aren't timed
	seed    uint64 // repeat r uses seed + r - 1
	era     *Era   // 1348 or 2026 rules
	machine string // this machine's name
	cpu     string // this machine's CPU
	dir     string // the folder results.csv is in
}

// numbers turns "1,2,4" into [1 2 4]. Anything that isn't a number above zero stops the program.
func numbers(text string) []int {
	var list []int
	for _, part := range strings.Split(text, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 {
			fmt.Println("not a number above zero:", part)
			os.Exit(2)
		}
		list = append(list, n)
	}
	return list
}

// eraNamed returns the 1348 or 2026 rules.
func eraNamed(year string) *Era {
	for _, era := range eras {
		if era.Year == year {
			return era
		}
	}
	fmt.Println("-era must be 2026 or 1348")
	os.Exit(2)
	return nil
}

// defaultCores are 1, 2, 4, 8 … up to this machine's cores, that number itself, and half of it.
// Windows counts hyperthreads as cores, so half is usually the number of real cores.
func defaultCores() []int {
	all := runtime.NumCPU()
	list := []int{all}
	if all >= 4 {
		list = append(list, all/2)
	}
	for n := 1; n < all; n *= 2 {
		list = append(list, n)
	}
	slices.Sort(list)
	return slices.Compact(list) // each number once
}

// runBench times every number of dots on every number of cores and saves each run as it finishes,
// so stopping it half way loses nothing.
func runBench(m *Map, o benchOptions) error {
	if len(o.agents) == 0 {
		o.agents = benchAgents
	}
	if len(o.cores) == 0 {
		o.cores = defaultCores()
	}
	if o.machine == "" {
		o.machine, _ = os.Hostname()
	}
	if o.cpu == "" {
		o.cpu = cpuName()
	}
	var coreList []int
	for _, n := range o.cores {
		if n > runtime.NumCPU() { // more goroutines than cores would only take turns
			fmt.Printf("skipping %d cores: this machine has %d\n", n, runtime.NumCPU())
			continue
		}
		coreList = append(coreList, n)
	}
	commit := gitCommit()
	date := time.Now().Format("2006-01-02 15:04")
	fmt.Printf("%s, %s, %d logical cores, commit %s\n", o.machine, o.cpu, runtime.NumCPU(), commit)
	fmt.Printf("%s rules: days %d to %d are timed, %d repeats\n\n", o.era.Year, o.fromDay, o.fromDay+o.days, o.repeats)

	numAgents = o.agents[0] // a short warm-up run that isn't timed
	benchRun(m, o.era, o.seed, runtime.NumCPU(), 5, 5)

	fmt.Printf("%10s  %5s  %6s  %5s  %10s  %8s  %8s  %7s\n", "dots", "cores", "repeat", "seed", "ms per day", "sick", "dead", "check")
	for _, agents := range o.agents {
		numAgents = agents
		for r := 1; r <= o.repeats; r++ {
			seed := o.seed + uint64(r-1) // the same outbreak for every number of cores
			firstDead := -1              // deaths in this outbreak on the first number of cores
			for _, n := range coreList {
				ms, sick, dead := benchRun(m, o.era, seed, n, o.fromDay, o.days)
				check := "same" // the same seed must give the same deaths on any number of cores
				if firstDead < 0 {
					firstDead, check = dead, ""
				} else if dead != firstDead {
					check = "DIFFERENT"
				}
				fmt.Printf("%10s  %5d  %6d  %5d  %10.2f  %8d  %8d  %7s\n", thousands(float64(agents)), n, r, seed, ms, sick, dead, check)
				row := []string{date, o.machine, o.cpu, strconv.Itoa(runtime.NumCPU()), commit, o.era.Year,
					strconv.Itoa(agents), strconv.Itoa(n), strconv.Itoa(r), strconv.FormatUint(seed, 10),
					strconv.Itoa(o.fromDay), strconv.Itoa(o.days), strconv.FormatFloat(ms, 'f', 3, 64), strconv.Itoa(dead)}
				if err := appendRow(filepath.Join(o.dir, benchFile), row); err != nil {
					return err
				}
			}
		}
		fmt.Println()
	}
	fmt.Printf("Added to %s. Now run: go run ./src -report %s\n", filepath.Join(o.dir, benchFile), o.dir)
	return nil
}

// benchRun sets up one outbreak, runs it to fromDay without timing, then times the next days.
// It returns the milliseconds per day, how many dots had plague when the timing began (so we can
// see the cores had real work), and the deaths at the end.
func benchRun(m *Map, era *Era, seed uint64, n, fromDay, days int) (ms float64, sick, dead int) {
	setCores(n)
	w := newWorld(m, era, seed)
	for w.Day < fromDay {
		w.Step()
	}
	c := w.counts()
	sick = c[ExposedBubonic] + c[ExposedPneumonic] + c[Bubonic] + c[Pneumonic]
	runtime.GC() // tidy memory now, so tidying up after the set-up isn't timed
	start := time.Now()
	for d := 0; d < days; d++ {
		w.Step()
	}
	ms = time.Since(start).Seconds() * 1000 / float64(days)
	return ms, sick, w.counts()[Dead]
}

// appendRow adds one row to the end of the results file. A new file gets the header first; an
// existing file is never overwritten.
func appendRow(name string, row []string) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	out := csv.NewWriter(file)
	if info, err := file.Stat(); err == nil && info.Size() == 0 { // a new file
		out.Write(benchHeader)
	}
	out.Write(row)
	out.Flush()
	return out.Error()
}

// gitCommit returns the short name of the current commit, or "" when git isn't available.
func gitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// cpuName asks the system for the CPU's name, e.g. "AMD Ryzen 5 4600H with Radeon Graphics".
// Each system keeps it somewhere different; if none answers, use -cpu.
func cpuName() string {
	var out []byte
	switch runtime.GOOS {
	case "windows": // in the registry
		out, _ = exec.Command("reg", "query", `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "/v", "ProcessorNameString").Output()
		if _, name, found := strings.Cut(string(out), "REG_SZ"); found {
			return strings.TrimSpace(name)
		}
	case "darwin": // a Mac
		out, _ = exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		return strings.TrimSpace(string(out))
	default: // Linux
		out, _ = os.ReadFile("/proc/cpuinfo")
		for _, line := range strings.Split(string(out), "\n") {
			if key, name, found := strings.Cut(line, ":"); found && strings.TrimSpace(key) == "model name" {
				return strings.TrimSpace(name)
			}
		}
	}
	return "unknown CPU"
}
