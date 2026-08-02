// Package render formats samples into vmstat's columns.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/trentas/vmstat-macos/internal/collect"
)

// Unit is the unit that memory and I/O values are reported in.
type Unit struct {
	Name  string
	Scale float64
}

var (
	UnitKiB = Unit{"KiB", 1024}
	UnitKB  = Unit{"kB", 1000}
	UnitMiB = Unit{"MiB", 1024 * 1024}
	UnitMB  = Unit{"MB", 1000 * 1000}
)

// ParseUnit accepts the same suffixes as Linux vmstat's -S flag.
func ParseUnit(s string) (Unit, error) {
	switch s {
	case "K":
		return UnitKiB, nil
	case "k":
		return UnitKB, nil
	case "M":
		return UnitMiB, nil
	case "m":
		return UnitMB, nil
	default:
		return Unit{}, fmt.Errorf("invalid unit %q (use k, K, m or M)", s)
	}
}

// Options controls the output format.
type Options struct {
	// Linux emits exactly Linux vmstat's columns. The metrics macOS does not
	// expose (buff, in, wa, st) are printed as zero, so that scripts consuming
	// column positions keep working.
	Linux bool
	// JSON emits one object per sample instead of the table.
	JSON bool
	// Unit is the unit for memory and I/O values.
	Unit Unit
	// HeaderEvery repeats the header every N rows. Zero prints it once.
	HeaderEvery int
	// NoHeader suppresses the header.
	NoHeader bool
}

// Renderer writes the output rows.
type Renderer struct {
	opts Options
	w    io.Writer
	rows int
	enc  *json.Encoder
}

func New(w io.Writer, opts Options) *Renderer {
	r := &Renderer{opts: opts, w: w}
	if opts.JSON {
		r.enc = json.NewEncoder(w)
	}
	return r
}

const (
	linuxGroups  = "procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----"
	linuxColumns = " r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st"
)

// group centers a label within a run of dashes of the requested width.
func group(label string, width int) string {
	if len(label) >= width {
		return label[:width]
	}
	pad := width - len(label)
	left := pad / 2
	return strings.Repeat("-", left) + label + strings.Repeat("-", pad-left)
}

// nativeWidths defines the width of each column in native mode.
var nativeWidths = struct {
	r, b                     int
	swpd, free, avail, cache int
	comp                     int
	si, so                   int
	bi, bo                   int
	cs                       int
	us, sy, id               int
}{
	r: 4, b: 4,
	swpd: 8, free: 8, avail: 8, cache: 8, comp: 8,
	si: 6, so: 6,
	bi: 7, bo: 7,
	cs: 7,
	us: 3, sy: 3, id: 3,
}

func nativeHeaders() (string, string) {
	w := nativeWidths
	procs := w.r + 1 + w.b
	mem := w.swpd + 1 + w.free + 1 + w.avail + 1 + w.cache + 1 + w.comp
	swap := w.si + 1 + w.so
	io := w.bi + 1 + w.bo
	system := w.cs
	cpu := w.us + 1 + w.sy + 1 + w.id

	groups := strings.Join([]string{
		group("procs", procs),
		group("memory", mem),
		group("swap", swap),
		group("io", io),
		group("system", system),
		group("cpu", cpu),
	}, " ")

	cols := fmt.Sprintf("%*s %*s %*s %*s %*s %*s %*s %*s %*s %*s %*s %*s %*s %*s %*s",
		w.r, "r", w.b, "b",
		w.swpd, "swpd", w.free, "free", w.avail, "avail", w.cache, "cache", w.comp, "comp",
		w.si, "si", w.so, "so",
		w.bi, "bi", w.bo, "bo",
		w.cs, "cs",
		w.us, "us", w.sy, "sy", w.id, "id")

	return groups, cols
}

func (r *Renderer) writeHeader() {
	if r.opts.NoHeader || r.opts.JSON {
		return
	}
	if r.opts.Linux {
		fmt.Fprintln(r.w, linuxGroups)
		fmt.Fprintln(r.w, linuxColumns)
		return
	}
	g, c := nativeHeaders()
	fmt.Fprintln(r.w, g)
	fmt.Fprintln(r.w, c)
}

// scale converts bytes into the configured unit.
func (r *Renderer) scale(bytes float64) uint64 {
	if bytes <= 0 {
		return 0
	}
	return uint64(bytes/r.opts.Unit.Scale + 0.5)
}

// Row writes one data row.
func (r *Renderer) Row(s collect.Sample, rt collect.Rates) error {
	if r.opts.JSON {
		return r.enc.Encode(newJSONRow(s, rt, r.opts.Unit))
	}

	if r.rows == 0 || (r.opts.HeaderEvery > 0 && r.rows%r.opts.HeaderEvery == 0) {
		r.writeHeader()
	}
	r.rows++

	if r.opts.Linux {
		// buff, in, wa and st do not exist on macOS; see the README.
		_, err := fmt.Fprintf(r.w, "%2d %2d %6d %6d %6d %6d %4d %4d %5d %5d %4d %4d %2d %2d %2d %2d %2d\n",
			s.Runnable, s.Blocked,
			r.scale(float64(s.SwapUsed)), r.scale(float64(s.Free)), 0, r.scale(float64(s.Cached)),
			r.scale(rt.SwapIn), r.scale(rt.SwapOut),
			r.scale(rt.BlockIn), r.scale(rt.BlockOut),
			0, int(rt.ContextSwitches+0.5),
			int(rt.User+0.5), int(rt.Sys+0.5), int(rt.Idle+0.5), 0, 0)
		return err
	}

	w := nativeWidths
	_, err := fmt.Fprintf(r.w, "%*d %*d %*d %*d %*d %*d %*d %*d %*d %*d %*d %*d %*d %*d %*d\n",
		w.r, s.Runnable, w.b, s.Blocked,
		w.swpd, r.scale(float64(s.SwapUsed)),
		w.free, r.scale(float64(s.Free)),
		w.avail, r.scale(float64(s.Available)),
		w.cache, r.scale(float64(s.Cached)),
		w.comp, r.scale(float64(s.Compressed)),
		w.si, r.scale(rt.SwapIn), w.so, r.scale(rt.SwapOut),
		w.bi, r.scale(rt.BlockIn), w.bo, r.scale(rt.BlockOut),
		w.cs, int(rt.ContextSwitches+0.5),
		w.us, int(rt.User+0.5), w.sy, int(rt.Sys+0.5), w.id, int(rt.Idle+0.5))
	return err
}

// jsonRow is the serialized form of a sample.
type jsonRow struct {
	Time     string `json:"time"`
	Unit     string `json:"unit"`
	Interval string `json:"interval"`

	Procs struct {
		Runnable   int `json:"r"`
		Blocked    int `json:"b"`
		Threads    int `json:"threads"`
		Total      int `json:"total"`
		Accessible int `json:"accessible"`
		Denied     int `json:"denied"`
	} `json:"procs"`

	Memory struct {
		Swapped    uint64 `json:"swpd"`
		Free       uint64 `json:"free"`
		Available  uint64 `json:"avail"`
		Cached     uint64 `json:"cache"`
		Compressed uint64 `json:"comp"`
		Active     uint64 `json:"active"`
		Inactive   uint64 `json:"inactive"`
		Wired      uint64 `json:"wired"`
	} `json:"memory"`

	Swap struct {
		In  uint64 `json:"si"`
		Out uint64 `json:"so"`
	} `json:"swap"`

	IO struct {
		BlockIn  uint64 `json:"bi"`
		BlockOut uint64 `json:"bo"`
	} `json:"io"`

	System struct {
		ContextSwitches uint64 `json:"cs"`
		Compressions    uint64 `json:"compressions"`
		Decompressions  uint64 `json:"decompressions"`
	} `json:"system"`

	CPU struct {
		User float64 `json:"us"`
		Sys  float64 `json:"sy"`
		Idle float64 `json:"id"`
	} `json:"cpu"`

	Load [3]float64 `json:"load"`
}

func newJSONRow(s collect.Sample, rt collect.Rates, u Unit) jsonRow {
	sc := func(v float64) uint64 {
		if v <= 0 {
			return 0
		}
		return uint64(v/u.Scale + 0.5)
	}

	var j jsonRow
	j.Time = s.Time.Format("2006-01-02T15:04:05Z07:00")
	j.Unit = u.Name
	j.Interval = rt.Interval.Round(1e6).String()

	j.Procs.Runnable = s.Runnable
	j.Procs.Blocked = s.Blocked
	j.Procs.Threads = s.Threads
	j.Procs.Total = s.ProcsTotal
	j.Procs.Accessible = s.ProcsAccessible
	j.Procs.Denied = s.ProcsDenied

	j.Memory.Swapped = sc(float64(s.SwapUsed))
	j.Memory.Free = sc(float64(s.Free))
	j.Memory.Available = sc(float64(s.Available))
	j.Memory.Cached = sc(float64(s.Cached))
	j.Memory.Compressed = sc(float64(s.Compressed))
	j.Memory.Active = sc(float64(s.Active))
	j.Memory.Inactive = sc(float64(s.Inactive))
	j.Memory.Wired = sc(float64(s.Wired))

	j.Swap.In = sc(rt.SwapIn)
	j.Swap.Out = sc(rt.SwapOut)
	j.IO.BlockIn = sc(rt.BlockIn)
	j.IO.BlockOut = sc(rt.BlockOut)

	j.System.ContextSwitches = uint64(rt.ContextSwitches + 0.5)
	j.System.Compressions = uint64(rt.Compressions + 0.5)
	j.System.Decompressions = uint64(rt.Decompressions + 0.5)

	j.CPU.User = round1(rt.User)
	j.CPU.Sys = round1(rt.Sys)
	j.CPU.Idle = round1(rt.Idle)

	j.Load = [3]float64{s.Load1, s.Load5, s.Load15}
	return j
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
