package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/trentas/vmstat-macos/internal/collect"
)

func TestParseUnit(t *testing.T) {
	cases := map[string]float64{"k": 1000, "K": 1024, "m": 1e6, "M": 1 << 20}
	for in, want := range cases {
		u, err := ParseUnit(in)
		if err != nil {
			t.Fatalf("ParseUnit(%q): %v", in, err)
		}
		if u.Scale != want {
			t.Errorf("ParseUnit(%q).Scale = %v, want %v", in, u.Scale, want)
		}
	}
	if _, err := ParseUnit("g"); err == nil {
		t.Error("ParseUnit(\"g\") should fail")
	}
}

// The --linux header must match procps byte for byte, or scripts that key off
// column positions will break.
func TestLinuxHeaderMatchesProcps(t *testing.T) {
	const wantGroups = "procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----"
	const wantCols = " r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st"

	if linuxGroups != wantGroups {
		t.Errorf("group header drifted:\n got %q\nwant %q", linuxGroups, wantGroups)
	}
	if linuxColumns != wantCols {
		t.Errorf("column header drifted:\n got %q\nwant %q", linuxColumns, wantCols)
	}
}

func sampleFixture() (collect.Sample, collect.Rates) {
	s := collect.Sample{
		Time:       time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Runnable:   3,
		Blocked:    1,
		Threads:    900,
		SwapUsed:   2048 * 1024, // 2048 KiB
		Free:       4096 * 1024,
		Available:  8192 * 1024,
		Cached:     1024 * 1024,
		Compressed: 512 * 1024,
	}
	r := collect.Rates{
		Interval:        time.Second,
		SwapIn:          1024,
		SwapOut:         2048,
		BlockIn:         4096,
		BlockOut:        8192,
		ContextSwitches: 1234,
		User:            10,
		Sys:             5,
		Idle:            85,
	}
	return s, r
}

func TestLinuxRowIsFixedWidth(t *testing.T) {
	s, rt := sampleFixture()
	var buf bytes.Buffer
	r := New(&buf, Options{Linux: true, Unit: UnitKiB})
	if err := r.Row(s, rt); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 2 header lines plus 1 row, got %d", len(lines))
	}
	if len(lines[1]) != len(lines[2]) {
		t.Errorf("row width %d does not match header width %d\nheader: %q\nrow:    %q",
			len(lines[2]), len(lines[1]), lines[1], lines[2])
	}

	fields := strings.Fields(lines[2])
	if len(fields) != 17 {
		t.Fatalf("expected 17 columns, got %d: %v", len(fields), fields)
	}
	// buff, in, wa, st are the columns macOS cannot supply.
	for _, idx := range []int{4, 10, 15, 16} {
		if fields[idx] != "0" {
			t.Errorf("column %d should be 0 (unavailable on macOS), got %q", idx, fields[idx])
		}
	}
	if fields[0] != "3" || fields[1] != "1" {
		t.Errorf("r/b = %q/%q, want 3/1", fields[0], fields[1])
	}
	if fields[2] != "2048" {
		t.Errorf("swpd = %q, want 2048", fields[2])
	}
}

func TestNativeHeaderAlignsWithRow(t *testing.T) {
	s, rt := sampleFixture()
	var buf bytes.Buffer
	r := New(&buf, Options{Unit: UnitKiB})
	if err := r.Row(s, rt); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for i, l := range lines {
		if len(l) != len(lines[0]) {
			t.Errorf("line %d width %d != group header width %d\n%q", i, len(l), len(lines[0]), l)
		}
	}
	// Native mode drops the columns macOS cannot fill, so it is 15 wide.
	if got := len(strings.Fields(lines[2])); got != 15 {
		t.Errorf("expected 15 columns in native mode, got %d", got)
	}
}

func TestUnitScaling(t *testing.T) {
	s, rt := sampleFixture()
	var buf bytes.Buffer
	// 4096 KiB of free memory is 4 MiB.
	r := New(&buf, Options{Linux: true, Unit: UnitMiB, NoHeader: true})
	if err := r.Row(s, rt); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(buf.String())
	if fields[3] != "4" {
		t.Errorf("free in MiB = %q, want 4", fields[3])
	}
}

func TestJSONOutput(t *testing.T) {
	s, rt := sampleFixture()
	var buf bytes.Buffer
	r := New(&buf, Options{JSON: true, Unit: UnitKiB})
	if err := r.Row(s, rt); err != nil {
		t.Fatal(err)
	}

	var row jsonRow
	if err := json.Unmarshal(buf.Bytes(), &row); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if row.Procs.Runnable != 3 || row.Procs.Blocked != 1 {
		t.Errorf("procs = %+v, want r=3 b=1", row.Procs)
	}
	if row.Memory.Free != 4096 {
		t.Errorf("free = %d, want 4096", row.Memory.Free)
	}
	if row.Unit != "KiB" {
		t.Errorf("unit = %q, want KiB", row.Unit)
	}
	if row.System.ContextSwitches != 1234 {
		t.Errorf("cs = %d, want 1234", row.System.ContextSwitches)
	}
}

func TestHeaderRepeats(t *testing.T) {
	s, rt := sampleFixture()
	var buf bytes.Buffer
	r := New(&buf, Options{Linux: true, Unit: UnitKiB, HeaderEvery: 2})
	for i := 0; i < 5; i++ {
		if err := r.Row(s, rt); err != nil {
			t.Fatal(err)
		}
	}
	// Rows 0, 2 and 4 each trigger a 2-line header.
	if got := strings.Count(buf.String(), "procs ---"); got != 3 {
		t.Errorf("header printed %d times, want 3", got)
	}
}

func TestGroupCentersLabel(t *testing.T) {
	if got := group("cpu", 9); got != "---cpu---" {
		t.Errorf("group = %q, want ---cpu---", got)
	}
	if got := group("memory", 6); got != "memory" {
		t.Errorf("group = %q, want memory", got)
	}
	if got := group("verylonglabel", 4); len(got) != 4 {
		t.Errorf("group should truncate to width 4, got %q", got)
	}
}
