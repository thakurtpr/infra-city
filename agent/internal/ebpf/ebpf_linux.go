// SPDX-License-Identifier: Apache-2.0
//go:build linux && !ebpf_full

package ebpf

import "os"

// linuxTracer is defined in flowconv.go (shared fallback); this file only
// selects it for the portable default build: no compiled objects, no
// cilium/ebpf in the compile graph. For the real loader, build with
// -tags ebpf_full after `make ebpf` (see ebpf/README.md).
func newTracer() Tracer {
	if disabledByEnv() {
		return &linuxTracer{reason: "disabled via INFRACITY_EBPF=off; using /proc fallback"}
	}
	// Privilege probe: eBPF needs CAP_BPF (or CAP_SYS_ADMIN on <5.8) + access to debugfs.
	if os.Geteuid() != 0 {
		return &linuxTracer{enabled: false, reason: "not root: eBPF disabled, using /proc fallback (run DaemonSet privileged for full fidelity)"}
	}
	if _, err := os.Stat("/sys/kernel/debug"); err != nil {
		return &linuxTracer{enabled: false, reason: "/sys/kernel/debug unavailable: using /proc fallback"}
	}
	// Without compiled objects in this portable build, report "ready but degraded".
	return &linuxTracer{enabled: false, reason: "eBPF objects not compiled in this build (see ebpf/README.md); using /proc fallback with identical edge schema"}
}
