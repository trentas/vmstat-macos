//go:build darwin

package collect

/*
#include <stdint.h>
#include <sys/types.h>
#include <sys/sysctl.h>
#include <mach/mach.h>
#include <mach/mach_host.h>

typedef struct {
	uint64_t page_size;
	uint64_t free, active, inactive, speculative, wired, purgeable;
	uint64_t external, internal;
	uint64_t compressor_pages;
	uint64_t compressions, decompressions;
	uint64_t pageins, pageouts, swapins, swapouts;
	uint64_t faults, cow_faults;
	uint64_t swap_total, swap_used;
} vm_snap_t;

// mach_host_self() returns a port reference on every call. In a sampler that
// runs indefinitely that leaks, so we acquire the port exactly once.
static host_t cached_host(void) {
	static host_t h = 0;
	if (h == 0) {
		h = mach_host_self();
	}
	return h;
}

static uint64_t sys_page_size(void) {
	int ps = 0;
	size_t len = sizeof(ps);
	if (sysctlbyname("hw.pagesize", &ps, &len, NULL, 0) == 0 && ps > 0) {
		return (uint64_t)ps;
	}
	return 4096;
}

static int vm_snapshot(vm_snap_t *o) {
	vm_statistics64_data_t v;
	mach_msg_type_number_t cnt = HOST_VM_INFO64_COUNT;
	if (host_statistics64(cached_host(), HOST_VM_INFO64, (host_info64_t)&v, &cnt) != KERN_SUCCESS) {
		return -1;
	}

	o->page_size = sys_page_size();

	// vm_stat(1) reports "Pages free" and "Pages speculative" separately, but the
	// kernel's free_count already includes speculative pages. Mirror the split.
	o->speculative = v.speculative_count;
	o->free = v.free_count >= v.speculative_count ? v.free_count - v.speculative_count : 0;

	o->active = v.active_count;
	o->inactive = v.inactive_count;
	o->wired = v.wire_count;
	o->purgeable = v.purgeable_count;
	o->external = v.external_page_count;
	o->internal = v.internal_page_count;
	o->compressor_pages = v.compressor_page_count;
	o->compressions = v.compressions;
	o->decompressions = v.decompressions;
	o->pageins = v.pageins;
	o->pageouts = v.pageouts;
	o->swapins = v.swapins;
	o->swapouts = v.swapouts;
	o->faults = v.faults;
	o->cow_faults = v.cow_faults;

	struct xsw_usage sw;
	size_t len = sizeof(sw);
	if (sysctlbyname("vm.swapusage", &sw, &len, NULL, 0) == 0) {
		o->swap_total = sw.xsu_total;
		o->swap_used = sw.xsu_used;
	}
	return 0;
}

static int boot_time_sec(int64_t *out) {
	struct timeval tv;
	size_t len = sizeof(tv);
	int mib[2] = {CTL_KERN, KERN_BOOTTIME};
	if (sysctl(mib, 2, &tv, &len, NULL, 0) != 0) {
		return -1;
	}
	*out = (int64_t)tv.tv_sec;
	return 0;
}
*/
import "C"

import (
	"errors"
	"time"
)

// vmStats mirrors host_statistics64(HOST_VM_INFO64) in page units, except for
// swap, which sysctl vm.swapusage already reports in bytes.
type vmStats struct {
	PageSize uint64

	Free        uint64
	Active      uint64
	Inactive    uint64
	Speculative uint64
	Wired       uint64
	Purgeable   uint64
	FileBacked  uint64 // external_page_count, the closest analogue to "cache"
	Anonymous   uint64 // internal_page_count

	CompressorPages uint64

	Compressions   uint64
	Decompressions uint64
	PageIns        uint64
	PageOuts       uint64
	SwapIns        uint64
	SwapOuts       uint64
	Faults         uint64
	CowFaults      uint64

	SwapTotal uint64
	SwapUsed  uint64
}

func readVMStats() (vmStats, error) {
	var c C.vm_snap_t
	if C.vm_snapshot(&c) != 0 {
		return vmStats{}, errors.New("host_statistics64(HOST_VM_INFO64) failed")
	}
	return vmStats{
		PageSize:        uint64(c.page_size),
		Free:            uint64(c.free),
		Active:          uint64(c.active),
		Inactive:        uint64(c.inactive),
		Speculative:     uint64(c.speculative),
		Wired:           uint64(c.wired),
		Purgeable:       uint64(c.purgeable),
		FileBacked:      uint64(c.external),
		Anonymous:       uint64(c.internal),
		CompressorPages: uint64(c.compressor_pages),
		Compressions:    uint64(c.compressions),
		Decompressions:  uint64(c.decompressions),
		PageIns:         uint64(c.pageins),
		PageOuts:        uint64(c.pageouts),
		SwapIns:         uint64(c.swapins),
		SwapOuts:        uint64(c.swapouts),
		Faults:          uint64(c.faults),
		CowFaults:       uint64(c.cow_faults),
		SwapTotal:       uint64(c.swap_total),
		SwapUsed:        uint64(c.swap_used),
	}, nil
}

// bootTime feeds vmstat's first line, which reports averages since boot.
func bootTime() (time.Time, error) {
	var sec C.int64_t
	if C.boot_time_sec(&sec) != 0 {
		return time.Time{}, errors.New("sysctl kern.boottime failed")
	}
	return time.Unix(int64(sec), 0), nil
}
