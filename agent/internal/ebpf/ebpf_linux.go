//go:build linux

package ebpf

import (
	"os"

	"github.com/infracity/infracity/agent/internal/netmon"
)

// linuxTracer attempts privileged eBPF, degrades to /proc when unavailable.
// The full cilium/ebpf loader (TC + kprobe socket tracing, PID->cgroup->pod
// attribution) is documented in ebpf/README.md and enabled by building with
// -tags ebpf_full once kernel headers + objects are present.
type linuxTracer struct {
	enabled bool
	reason  string
}

func newTracer() Tracer {
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

func (t *linuxTracer) Enabled() bool { return t.enabled }
func (t *linuxTracer) Reason() string { return t.reason }
func (t *linuxTracer) Sample() ([]netmon.Flow, error) {
	return netmon.SampleProcNet()
}
func (t *linuxTracer) Close() error { return nil }
