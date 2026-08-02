//go:build !darwin

// Command vmstat is implemented for macOS only: it reads counters from Mach,
// libproc and IOKit, which do not exist elsewhere. On Linux, use procps vmstat.
package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Fprintf(os.Stderr,
		"vmstat-macos only runs on macOS (current system: %s). On Linux, use procps vmstat.\n",
		runtime.GOOS)
	os.Exit(1)
}
