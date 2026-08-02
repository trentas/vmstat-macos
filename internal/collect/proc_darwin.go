//go:build darwin

package collect

/*
#include <stdint.h>
#include <stdlib.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <mach/thread_info.h>

typedef struct {
	int32_t pid;
	uint32_t csw;
} pid_csw_t;

typedef struct {
	int32_t procs_total;
	int32_t procs_ok;
	int32_t procs_denied;
	int32_t threads;
	int32_t running;
	int32_t uninterruptible;
} proc_snap_t;

// proc_snapshot walks every visible pid. Returns the number of (pid, csw)
// pairs written into arr, or -1 on failure.
//
// scan_threads toggles the per-thread walk, needed only for the "b" column
// (threads blocked in uninterruptible I/O). It costs ~6ms across 3500 threads,
// versus ~2ms for the task-only walk.
static int proc_snapshot(proc_snap_t *o, pid_csw_t *arr, int cap, int scan_threads) {
	int bytes = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
	if (bytes <= 0) {
		return -1;
	}
	// The pid set can grow between sizing the buffer and reading into it.
	int room = bytes + 128 * (int)sizeof(pid_t);
	pid_t *pids = (pid_t *)malloc((size_t)room);
	if (pids == NULL) {
		return -1;
	}
	bytes = proc_listpids(PROC_ALL_PIDS, 0, pids, room);
	if (bytes <= 0) {
		free(pids);
		return -1;
	}
	int n = bytes / (int)sizeof(pid_t);
	int written = 0;

	for (int i = 0; i < n; i++) {
		if (pids[i] <= 0) {
			continue;
		}
		o->procs_total++;

		struct proc_taskinfo ti;
		if (proc_pidinfo(pids[i], PROC_PIDTASKINFO, 0, &ti, sizeof(ti)) != (int)sizeof(ti)) {
			// Not permitted (process owned by another uid) or already exited.
			o->procs_denied++;
			continue;
		}
		o->procs_ok++;
		o->threads += ti.pti_threadnum;
		o->running += ti.pti_numrunning;

		if (written < cap) {
			arr[written].pid = pids[i];
			arr[written].csw = (uint32_t)ti.pti_csw;
			written++;
		}

		if (!scan_threads) {
			continue;
		}
		uint64_t tids[1024];
		int tb = proc_pidinfo(pids[i], PROC_PIDLISTTHREADS, 0, tids, sizeof(tids));
		if (tb <= 0) {
			continue;
		}
		int tn = tb / (int)sizeof(uint64_t);
		for (int t = 0; t < tn; t++) {
			struct proc_threadinfo th;
			if (proc_pidinfo(pids[i], PROC_PIDTHREADINFO, tids[t], &th, sizeof(th)) != (int)sizeof(th)) {
				continue;
			}
			if (th.pth_run_state == TH_STATE_UNINTERRUPTIBLE) {
				o->uninterruptible++;
			}
		}
	}
	free(pids);
	return written;
}
*/
import "C"

import "errors"

type procStats struct {
	Total      int
	Accessible int
	Denied     int
	Threads    int
	Runnable   int // Mach TH_STATE_RUNNING, the analogue of Linux's "r"
	Blocked    int // Mach TH_STATE_UNINTERRUPTIBLE, the analogue of "b"

	// Per-pid csw, used to reconcile the total across samples.
	csw map[int32]uint32
}

// procScanner keeps the previous sample's csw so it can compute a correct
// delta even as processes are created and exit between samples.
type procScanner struct {
	scanThreads bool
	buf         []C.pid_csw_t
	prev        map[int32]uint32
	// The running total is synthetic: we accumulate only reconciled deltas, so
	// that exiting processes never produce a negative delta.
	total uint64
}

func newProcScanner(scanThreads bool) *procScanner {
	return &procScanner{
		scanThreads: scanThreads,
		buf:         make([]C.pid_csw_t, 8192),
		prev:        make(map[int32]uint32),
	}
}

func (s *procScanner) sample() (procStats, error) {
	var snap C.proc_snap_t
	scan := C.int(0)
	if s.scanThreads {
		scan = 1
	}

	written := C.proc_snapshot(&snap, &s.buf[0], C.int(len(s.buf)), scan)
	if written < 0 {
		return procStats{}, errors.New("proc_listpids failed")
	}
	// If the buffer filled up, grow it for the next sample instead of truncating.
	if int(written) == len(s.buf) {
		s.buf = make([]C.pid_csw_t, len(s.buf)*2)
	}

	cur := make(map[int32]uint32, int(written))
	var delta uint64
	for i := 0; i < int(written); i++ {
		pid := int32(s.buf[i].pid)
		csw := uint32(s.buf[i].csw)
		cur[pid] = csw

		if before, ok := s.prev[pid]; ok {
			if csw >= before {
				delta += uint64(csw - before)
			} else {
				// Recycled pid: the counter restarted.
				delta += uint64(csw)
			}
		} else {
			// New process: all of its csw happened within this interval.
			delta += uint64(csw)
		}
	}
	s.prev = cur
	s.total += delta

	return procStats{
		Total:      int(snap.procs_total),
		Accessible: int(snap.procs_ok),
		Denied:     int(snap.procs_denied),
		Threads:    int(snap.threads),
		Runnable:   int(snap.running),
		Blocked:    int(snap.uninterruptible),
		csw:        cur,
	}, nil
}

// contextSwitches returns the reconciled total since the collector started.
func (s *procScanner) contextSwitches() uint64 { return s.total }
