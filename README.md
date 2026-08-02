# vmstat-macos

[![CI](https://github.com/trentas/vmstat-macos/actions/workflows/ci.yml/badge.svg)](https://github.com/trentas/vmstat-macos/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/trentas/vmstat-macos.svg)](https://pkg.go.dev/github.com/trentas/vmstat-macos)
[![Go Report Card](https://goreportcard.com/badge/github.com/trentas/vmstat-macos)](https://goreportcard.com/report/github.com/trentas/vmstat-macos)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Linux's `vmstat`, for macOS. A single static binary with no daemon that samples
memory, swap, block I/O, context switches and CPU, and prints them in the column
layout you already know.

macOS ships `vm_stat`, which is a different tool: it dumps cumulative Mach
counters with no deltas, no interval sampling and no columns. There is no
`vmstat` on macOS, and there is no port of it — `procps` reads `/proc`, which
does not exist here, and Homebrew's `sysstat` is `depends_on :linux`.

```console
$ vmstat 1
--procs-- -------------------memory------------------- ----swap----- ------io------- system- ----cpu----
   r    b     swpd     free    avail    cache     comp     si     so      bi      bo      cs  us  sy  id
   3    0  5724032    63888  4990528  2480112  3345344    589   1073   18170    5077    4490  17   7  76
   2    0  5724032   103552  4894720  2364336    345088    462      0     509      56    3338   6   2  91
   7    0  5724032    66256  4901232  2374352    345088     64      0     112   23691    3841   5   3  92
```

The first line is the average since boot; every line after it is a delta over
the interval — same semantics as Linux.

## Install

```sh
brew install trentas/tap/vmstat-macos
```

Or from source (requires the Xcode command line tools, since the collector is cgo):

```sh
go install github.com/trentas/vmstat-macos@latest
```

## Usage

```
vmstat [options] [delay [count]]

  -S <k|K|m|M>      value unit (k=1000, K=1024, m=10^6, M=2^20). Default: K
  --linux           emit exactly the columns of Linux vmstat
  --json            emit one JSON object per sample
  -n                print the header only once
  --header-every N  repeat the header every N rows
  --no-blocked      skip the thread walk for the "b" column (~3x cheaper)
  -q                suppress warnings on stderr
```

`--linux` gives byte-for-byte the procps header and column widths, so existing
parsers keep working:

```console
$ vmstat --linux 1 3
procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----
 r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st
 2  0 5625728  71072      0 2364752  594 1069 18126  5062    0 4491 17  7 76  0  0
 5  0 5625728  81744      0 2773696    0    0  1032 390499    0 3815  7  5 88  0  0
```

`--json` emits one object per sample, for logging and dashboards:

```console
$ vmstat --json 1 | jq -c '{t:.time, r:.procs.r, bo:.io.bo, id:.cpu.id}'
{"t":"2026-08-01T21:34:02-03:00","r":3,"bo":5077,"id":76}
```

## Where the numbers come from

Linux `vmstat` reads everything from procfs. macOS has no procfs, so each column
is sourced from Mach, libproc, sysctl or IOKit:

| Column | Source on macOS | Fidelity |
|---|---|---|
| `r` | `proc_pidinfo(PROC_PIDTASKINFO)` → `pti_numrunning` | runnable threads |
| `b` | `PROC_PIDTHREADINFO` → `TH_STATE_UNINTERRUPTIBLE` | exact |
| `swpd` | `sysctl vm.swapusage` → `xsu_used` | exact |
| `free` | `host_statistics64(HOST_VM_INFO64)` → `free_count` | exact |
| `avail` | `free + inactive + speculative + purgeable` | native column |
| `cache` | `external_page_count` (file-backed pages) | close analogue |
| `comp` | `compressor_page_count` | native column |
| `si` / `so` | `swapins` / `swapouts` × page size | exact |
| `bi` / `bo` | IOKit `IOBlockStorageDriver` → `Bytes (Read/Write)` | exact |
| `cs` | Σ `pti_csw` across all pids, reconciled per sample | see caveats |
| `us` / `sy` / `id` | `host_statistics(HOST_CPU_LOAD_INFO)` | exact |
| `buff` | — | **not available** |
| `in` | — | **not available** |
| `wa` | — | **not available** |
| `st` | — | not applicable |

### Why four columns are missing

- **`buff`** — macOS has a unified buffer cache; there is no separate buffer
  pool to report. The concept does not exist.
- **`in`** (interrupts) — the kernel exposes no public counter.
  `host_processor_info` returns per-CPU ticks only. The counts exist inside
  `powermetrics --samplers interrupts` and kdebug, both root-only.
- **`wa`** (iowait) — `HOST_CPU_LOAD_INFO` accounts only USER, SYSTEM, IDLE and
  NICE. There is no honest approximation, so it is reported as zero rather than
  invented.
- **`st`** (steal) — no hypervisor steal accounting; not applicable.

Under `--linux` these print as `0` to keep column positions stable. Native mode
omits them entirely rather than showing a column that is always zero.

This is not a shortcoming of the approach: Performance Co-Pilot's actively
maintained Darwin PMDA hits exactly the same wall, exposing 13 of the 25 metrics
its `pmstat` needs, and missing `kernel.all.intr`, `kernel.all.pswitch`,
`kernel.all.cpu.wait.total` and `mem.util.bufmem` for the same reasons.

## Caveats worth knowing

**Privileges.** Without root, `proc_pidinfo` cannot read processes owned by
other users — typically 25-30% of them. That undercounts `r`, `b` and `cs`.
Every other column is unaffected. The tool warns on stderr when this happens;
run under `sudo` for full coverage, or pass `-q` to silence the warning.

Do **not** make the binary setuid root. A Go program is a poor candidate for
it: the runtime spawns threads before `main` runs, so privileges cannot be
dropped early and cleanly, and any bug in the cgo collector would become a
local root escalation — a steep price for a more complete `cs` column. It is
also pointless in the default install location, since a setuid binary sitting
in a user-writable directory can simply be replaced.

If you want full coverage without typing a password, scope a sudoers rule to
the binary instead. Install it somewhere only root can write first:

```sh
sudo make install PREFIX=/usr/local
sudo tee /etc/sudoers.d/vmstat >/dev/null <<'EOF'
yourusername ALL=(root) NOPASSWD: /usr/local/bin/vmstat
EOF
sudo chmod 0440 /etc/sudoers.d/vmstat
```

That keeps the elevation explicit and per-invocation, and it is auditable —
unlike a setuid bit, which grants it to every caller, always.

**`free` is not what you think.** On macOS free memory routinely sits near zero
on a perfectly healthy machine, because inactive, speculative and purgeable
pages are all reclaimable under pressure. The `avail` column is the number to
watch. Native mode shows both; `--linux` shows only `free`, matching Linux.

**Memory pressure shows up as compression, not swap.** Before macOS swaps, it
compresses. A machine can be under real pressure with `si`/`so` at zero while
`comp` climbs. That is why native mode carries a `comp` column.

**Page size is 16 KiB on Apple Silicon**, not 4 KiB. All conversions read
`hw.pagesize` rather than assuming a constant.

**P-cores and E-cores.** `HOST_CPU_LOAD_INFO` aggregates all cores, so one tick
of an efficiency core is not one tick of a performance core. CPU percentages
are not directly comparable to a Linux machine's.

## Cost

Sampling is cheap enough to run continuously at a 1s interval:

| Source | Cost |
|---|---|
| `host_statistics64` + `HOST_CPU_LOAD_INFO` | 0.011 ms |
| task walk (~700 pids) | 1.8 ms |
| thread walk (~3500 threads, for `b`) | 5.8 ms |

Pass `--no-blocked` to skip the thread walk if you do not need the `b` column.

## Development

```sh
make build     # build ./bin/vmstat
make test      # go test ./...
make check     # gofmt, go vet, tests
```

The collector is cgo, so the Xcode command line tools are required to build.

## License

MIT
