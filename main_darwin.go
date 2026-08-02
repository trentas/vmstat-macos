//go:build darwin

// Command vmstat reports virtual memory, I/O, process and CPU statistics on
// macOS, using the column layout of Linux's vmstat.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/trentas/vmstat-macos/internal/collect"
	"github.com/trentas/vmstat-macos/internal/render"
)

// version is overridden at build time via -ldflags.
var version = "dev"

const usage = `vmstat - virtual memory, I/O, process and CPU statistics for macOS

Usage:
  vmstat [options] [delay [count]]

With no delay, prints a single line holding the averages since boot, just like
Linux's vmstat. With a delay, each subsequent line is a delta over that period.

Options:
  -S <k|K|m|M>      value unit (k=1000, K=1024, m=10^6, M=2^20). Default: K
  --linux           emit exactly the columns of Linux vmstat
  --json            emit one JSON object per sample
  -n                print the header only once
  --header-every N  repeat the header every N rows
  --no-blocked      skip the thread walk for the "b" column (~3x cheaper)
  -q                suppress warnings on stderr
  -V, --version     print the version
  -h, --help        print this help

Columns macOS does not provide: buff, in (interrupts), wa (iowait) and st
(steal). The kernel exposes no counter for them; under --linux they are printed
as zero. See the README for the full column-to-source mapping.
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vmstat:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("vmstat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	var (
		unitFlag    = fs.String("S", "K", "value unit")
		linuxMode   = fs.Bool("linux", false, "emit exactly the columns of Linux vmstat")
		jsonMode    = fs.Bool("json", false, "JSON output")
		headerOnce  = fs.Bool("n", false, "print the header only once")
		headerEvery = fs.Int("header-every", 0, "repeat the header every N rows")
		noBlocked   = fs.Bool("no-blocked", false, "skip the thread walk for the b column")
		quiet       = fs.Bool("q", false, "suppress warnings")
		showVer     = fs.Bool("version", false, "print the version")
		showVerV    = fs.Bool("V", false, "print the version")
	)

	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVer || *showVerV {
		fmt.Printf("vmstat-macos %s\n", version)
		return nil
	}

	unit, err := render.ParseUnit(*unitFlag)
	if err != nil {
		return err
	}

	interval, count, err := parseArgs(fs.Args())
	if err != nil {
		return err
	}

	// By default the header repeats every 24 rows during continuous output,
	// which keeps the columns readable in a typical terminal.
	every := *headerEvery
	if every == 0 && !*headerOnce && interval > 0 {
		every = 24
	}

	collector, err := collect.New(!*noBlocked)
	if err != nil {
		return err
	}
	r := render.New(os.Stdout, render.Options{
		Linux:       *linuxMode,
		JSON:        *jsonMode,
		Unit:        unit,
		HeaderEvery: every,
	})

	prev, err := collector.Sample()
	if err != nil {
		return err
	}
	if !*quiet {
		warnPartialVisibility(prev)
	}

	// First line: averages since boot, as Linux vmstat does.
	if err := r.Row(prev, collect.SinceBootRates(prev)); err != nil {
		return err
	}
	if interval <= 0 || count == 1 {
		return nil
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	emitted := 1
	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			cur, err := collector.Sample()
			if err != nil {
				return err
			}
			if err := r.Row(cur, collect.ComputeRates(prev, cur)); err != nil {
				return err
			}
			prev = cur
			emitted++
			if count > 0 && emitted >= count {
				return nil
			}
		}
	}
}

// parseArgs interprets the positional arguments [delay [count]].
func parseArgs(args []string) (time.Duration, int, error) {
	switch len(args) {
	case 0:
		return 0, 1, nil
	case 1, 2:
		secs, err := strconv.ParseFloat(args[0], 64)
		if err != nil || secs <= 0 {
			return 0, 0, fmt.Errorf("invalid delay: %q", args[0])
		}
		count := 0 // zero means run indefinitely
		if len(args) == 2 {
			count, err = strconv.Atoi(args[1])
			if err != nil || count <= 0 {
				return 0, 0, fmt.Errorf("invalid count: %q", args[1])
			}
		}
		return time.Duration(secs * float64(time.Second)), count, nil
	default:
		return 0, 0, fmt.Errorf("too many arguments")
	}
}

// warnPartialVisibility flags the case where missing privileges hide processes,
// which undercounts the r, b and cs columns.
func warnPartialVisibility(s collect.Sample) {
	if s.ProcsDenied == 0 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"vmstat: %d of %d processes not visible without privileges; r, b and cs are undercounted (run under sudo for full coverage)\n",
		s.ProcsDenied, s.ProcsTotal)
}
