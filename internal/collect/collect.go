//go:build darwin

// Package collect gathers the system statistics that Linux's vmstat reads from
// /proc, sourcing them instead from Mach, libproc, sysctl and IOKit.
package collect

import (
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
)

// Sample is a point-in-time view of the system. Memory fields and process
// counts describe the instant of collection; everything else is a monotonic
// counter accumulated since boot.
type Sample struct {
	Time     time.Time
	Uptime   time.Duration
	PageSize uint64

	// Memory, in bytes.
	Free        uint64
	Active      uint64
	Inactive    uint64
	Speculative uint64
	Wired       uint64
	Purgeable   uint64
	Cached      uint64 // file-backed pages, the closest analogue to "cache"
	Anonymous   uint64
	Compressed  uint64 // memory held by the compressor
	Available   uint64 // free + inactive + speculative + purgeable
	SwapTotal   uint64
	SwapUsed    uint64

	// Counters accumulated since boot, in bytes.
	SwapIn  uint64
	SwapOut uint64
	PageIn  uint64
	PageOut uint64

	// Counters accumulated since boot, in events.
	Compressions    uint64
	Decompressions  uint64
	Faults          uint64
	ContextSwitches uint64

	DiskRead   uint64 // bytes
	DiskWrite  uint64 // bytes
	DiskReads  uint64 // operations
	DiskWrites uint64 // operations

	// CPU, in seconds accumulated since boot.
	CPUUser float64
	CPUNice float64
	CPUSys  float64
	CPUIdle float64

	// Process snapshot.
	Runnable        int
	Blocked         int
	Threads         int
	ProcsTotal      int
	ProcsAccessible int
	ProcsDenied     int

	Load1, Load5, Load15 float64
}

// CPUTotal is the sum of CPU seconds accounted for since boot.
func (s Sample) CPUTotal() float64 {
	return s.CPUUser + s.CPUNice + s.CPUSys + s.CPUIdle
}

// Rates holds the values derived from two consecutive samples.
type Rates struct {
	Interval time.Duration

	SwapIn  float64 // bytes/s
	SwapOut float64 // bytes/s
	PageIn  float64 // bytes/s
	PageOut float64 // bytes/s

	BlockIn  float64 // bytes/s read from block devices
	BlockOut float64 // bytes/s written to block devices

	ContextSwitches float64 // per second
	Compressions    float64 // per second
	Decompressions  float64 // per second

	// CPU percentages, summing to 100.
	User float64
	Sys  float64
	Idle float64
}

func perSecond(cur, prev uint64, secs float64) float64 {
	if secs <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / secs
}

// ComputeRates derives the rates between two samples.
func ComputeRates(prev, cur Sample) Rates {
	interval := cur.Time.Sub(prev.Time)
	secs := interval.Seconds()

	r := Rates{
		Interval:        interval,
		SwapIn:          perSecond(cur.SwapIn, prev.SwapIn, secs),
		SwapOut:         perSecond(cur.SwapOut, prev.SwapOut, secs),
		PageIn:          perSecond(cur.PageIn, prev.PageIn, secs),
		PageOut:         perSecond(cur.PageOut, prev.PageOut, secs),
		BlockIn:         perSecond(cur.DiskRead, prev.DiskRead, secs),
		BlockOut:        perSecond(cur.DiskWrite, prev.DiskWrite, secs),
		ContextSwitches: perSecond(cur.ContextSwitches, prev.ContextSwitches, secs),
		Compressions:    perSecond(cur.Compressions, prev.Compressions, secs),
		Decompressions:  perSecond(cur.Decompressions, prev.Decompressions, secs),
	}
	r.User, r.Sys, r.Idle = cpuShares(
		cur.CPUUser+cur.CPUNice-prev.CPUUser-prev.CPUNice,
		cur.CPUSys-prev.CPUSys,
		cur.CPUIdle-prev.CPUIdle,
	)
	return r
}

// SinceBootRates reproduces vmstat's first line, the average since boot.
func SinceBootRates(cur Sample) Rates {
	secs := cur.Uptime.Seconds()
	r := Rates{
		Interval:        cur.Uptime,
		SwapIn:          perSecond(cur.SwapIn, 0, secs),
		SwapOut:         perSecond(cur.SwapOut, 0, secs),
		PageIn:          perSecond(cur.PageIn, 0, secs),
		PageOut:         perSecond(cur.PageOut, 0, secs),
		BlockIn:         perSecond(cur.DiskRead, 0, secs),
		BlockOut:        perSecond(cur.DiskWrite, 0, secs),
		ContextSwitches: perSecond(cur.ContextSwitches, 0, secs),
		Compressions:    perSecond(cur.Compressions, 0, secs),
		Decompressions:  perSecond(cur.Decompressions, 0, secs),
	}
	r.User, r.Sys, r.Idle = cpuShares(cur.CPUUser+cur.CPUNice, cur.CPUSys, cur.CPUIdle)
	return r
}

func cpuShares(user, sys, idle float64) (u, s, i float64) {
	total := user + sys + idle
	if total <= 0 {
		return 0, 0, 100
	}
	return user / total * 100, sys / total * 100, idle / total * 100
}

// Collector produces successive samples, holding the state needed to reconcile
// the context-switch counter across them.
type Collector struct {
	scanner *procScanner
	boot    time.Time
}

// New creates a collector. scanBlocked enables the per-thread walk required for
// the "b" column; with it off, collection is roughly 3x cheaper.
func New(scanBlocked bool) (*Collector, error) {
	boot, err := bootTime()
	if err != nil {
		return nil, err
	}
	return &Collector{scanner: newProcScanner(scanBlocked), boot: boot}, nil
}

// Sample collects a full snapshot.
func (c *Collector) Sample() (Sample, error) {
	now := time.Now()

	vm, err := readVMStats()
	if err != nil {
		return Sample{}, err
	}
	ps, err := c.scanner.sample()
	if err != nil {
		return Sample{}, err
	}

	times, err := cpu.Times(false)
	if err != nil {
		return Sample{}, fmt.Errorf("cpu.Times: %w", err)
	}
	if len(times) == 0 {
		return Sample{}, fmt.Errorf("cpu.Times returned no data")
	}
	ct := times[0]

	s := Sample{
		Time:     now,
		Uptime:   now.Sub(c.boot),
		PageSize: vm.PageSize,

		Free:        vm.Free * vm.PageSize,
		Active:      vm.Active * vm.PageSize,
		Inactive:    vm.Inactive * vm.PageSize,
		Speculative: vm.Speculative * vm.PageSize,
		Wired:       vm.Wired * vm.PageSize,
		Purgeable:   vm.Purgeable * vm.PageSize,
		Cached:      vm.FileBacked * vm.PageSize,
		Anonymous:   vm.Anonymous * vm.PageSize,
		Compressed:  vm.CompressorPages * vm.PageSize,
		SwapTotal:   vm.SwapTotal,
		SwapUsed:    vm.SwapUsed,

		SwapIn:  vm.SwapIns * vm.PageSize,
		SwapOut: vm.SwapOuts * vm.PageSize,
		PageIn:  vm.PageIns * vm.PageSize,
		PageOut: vm.PageOuts * vm.PageSize,

		Compressions:    vm.Compressions,
		Decompressions:  vm.Decompressions,
		Faults:          vm.Faults,
		ContextSwitches: c.scanner.contextSwitches(),

		CPUUser: ct.User,
		CPUNice: ct.Nice,
		CPUSys:  ct.System,
		CPUIdle: ct.Idle,

		Runnable:        ps.Runnable,
		Blocked:         ps.Blocked,
		Threads:         ps.Threads,
		ProcsTotal:      ps.Total,
		ProcsAccessible: ps.Accessible,
		ProcsDenied:     ps.Denied,
	}

	// On macOS "free" is misleading: inactive, speculative and purgeable pages are
	// all reclaimable under pressure. Available is the number that actually matters.
	s.Available = s.Free + s.Inactive + s.Speculative + s.Purgeable

	// Disk: IOKit by way of gopsutil, summed across every block storage driver.
	if io, err := disk.IOCounters(); err == nil {
		for _, d := range io {
			s.DiskRead += d.ReadBytes
			s.DiskWrite += d.WriteBytes
			s.DiskReads += d.ReadCount
			s.DiskWrites += d.WriteCount
		}
	}

	if l, err := load.Avg(); err == nil {
		s.Load1, s.Load5, s.Load15 = l.Load1, l.Load5, l.Load15
	}

	return s, nil
}
