//go:build darwin

package collect

import (
	"math"
	"testing"
	"time"
)

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestComputeRatesDividesByInterval(t *testing.T) {
	base := time.Now()
	prev := Sample{Time: base, DiskRead: 1000, DiskWrite: 500, SwapIn: 200, ContextSwitches: 10_000}
	cur := Sample{Time: base.Add(2 * time.Second), DiskRead: 5000, DiskWrite: 900, SwapIn: 600, ContextSwitches: 30_000}

	r := ComputeRates(prev, cur)
	approx(t, "BlockIn", r.BlockIn, 2000)  // (5000-1000)/2s
	approx(t, "BlockOut", r.BlockOut, 200) // (900-500)/2s
	approx(t, "SwapIn", r.SwapIn, 200)     // (600-200)/2s
	approx(t, "ContextSwitches", r.ContextSwitches, 10_000)
}

// A counter that goes backwards must not produce a negative rate. This happens
// in practice when a process holding a large csw count exits between samples.
func TestPerSecondClampsCounterRegression(t *testing.T) {
	if got := perSecond(5, 100, 1); got != 0 {
		t.Errorf("perSecond with regressed counter = %v, want 0", got)
	}
	if got := perSecond(100, 5, 0); got != 0 {
		t.Errorf("perSecond with zero interval = %v, want 0", got)
	}
}

func TestCPUSharesSumTo100(t *testing.T) {
	u, s, i := cpuShares(20, 10, 70)
	approx(t, "user", u, 20)
	approx(t, "sys", s, 10)
	approx(t, "idle", i, 70)

	if u, s, i := cpuShares(0, 0, 0); u != 0 || s != 0 || i != 100 {
		t.Errorf("cpuShares with no ticks = (%v, %v, %v), want (0, 0, 100)", u, s, i)
	}
}

// The first line of vmstat is an average over uptime, not over an interval.
func TestSinceBootRatesUsesUptime(t *testing.T) {
	s := Sample{
		Uptime:          100 * time.Second,
		DiskRead:        100_000,
		ContextSwitches: 500_000,
		CPUUser:         25,
		CPUSys:          25,
		CPUIdle:         50,
	}
	r := SinceBootRates(s)
	approx(t, "BlockIn", r.BlockIn, 1000)
	approx(t, "ContextSwitches", r.ContextSwitches, 5000)
	approx(t, "User", r.User, 25)
	approx(t, "Idle", r.Idle, 50)
}

// The collector must produce coherent readings against the live system.
func TestCollectorSampleIsSane(t *testing.T) {
	c, err := New(true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s, err := c.Sample()
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}

	if s.PageSize == 0 || s.PageSize&(s.PageSize-1) != 0 {
		t.Errorf("PageSize = %d, want a non-zero power of two", s.PageSize)
	}
	if s.ProcsTotal < 2 {
		t.Errorf("ProcsTotal = %d, want at least 2", s.ProcsTotal)
	}
	if s.ProcsAccessible+s.ProcsDenied != s.ProcsTotal {
		t.Errorf("accessible(%d) + denied(%d) != total(%d)",
			s.ProcsAccessible, s.ProcsDenied, s.ProcsTotal)
	}
	if s.Threads < s.ProcsAccessible {
		t.Errorf("Threads = %d, want at least one per accessible process (%d)", s.Threads, s.ProcsAccessible)
	}
	if s.CPUTotal() <= 0 {
		t.Errorf("CPUTotal = %v, want > 0", s.CPUTotal())
	}
	if s.Available < s.Free {
		t.Errorf("Available(%d) must include Free(%d)", s.Available, s.Free)
	}
	if s.Uptime <= 0 {
		t.Errorf("Uptime = %v, want > 0", s.Uptime)
	}
}

// Counters that are monotonic since boot must never shrink between samples.
func TestCountersAreMonotonic(t *testing.T) {
	c, err := New(false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := c.Sample()
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	second, err := c.Sample()
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}

	cases := []struct {
		name string
		a, b uint64
	}{
		{"PageIn", first.PageIn, second.PageIn},
		{"SwapIn", first.SwapIn, second.SwapIn},
		{"DiskRead", first.DiskRead, second.DiskRead},
		{"DiskWrite", first.DiskWrite, second.DiskWrite},
		{"Compressions", first.Compressions, second.Compressions},
		{"ContextSwitches", first.ContextSwitches, second.ContextSwitches},
	}
	for _, tc := range cases {
		if tc.b < tc.a {
			t.Errorf("%s went backwards: %d -> %d", tc.name, tc.a, tc.b)
		}
	}
	if !second.Time.After(first.Time) {
		t.Error("sample timestamps did not advance")
	}
}
