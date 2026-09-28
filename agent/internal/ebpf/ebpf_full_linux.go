// SPDX-License-Identifier: Apache-2.0
//go:build linux && ebpf_full

package ebpf

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"

	"github.com/infracity/infracity/agent/internal/netmon"
)

//go:embed bpf/sock-trace.bpf.o
var bpfObjects []byte

// fullTracer loads the compiled CO-RE probe and consumes flow events.
// Sample() uses delete-after-read: each call returns connections observed
// since the previous call, so counters are per-interval, never cumulative.
// TCX byte accounting is best-effort: if TCX attach fails (old kernel),
// the tracepoint still reports connections and PIDs, bytes stay zero.
type fullTracer struct {
	mu      sync.Mutex
	coll    *ebpf.Collection
	tp      link.Link
	flows   *ebpf.Map
	tcx     map[tcxKey]link.Link
	reason  string
	tcxNote string
	closed  bool
}

// tcxKey identifies one direction attachment on one interface.
type tcxKey struct {
	ifindex int
	ingress bool
}

func newTracer() Tracer {
	if disabledByEnv() {
		return &linuxTracer{reason: "disabled via INFRACITY_EBPF=off; using /proc fallback"}
	}
	// No euid pre-check: bpf() is gated by CAP_BPF/CAP_SYS_ADMIN, not uid,
	// so attempt the load and let the kernel decide. Any failure degrades
	// with the wrapped reason below.
	t, err := loadFull()
	if err != nil {
		return &linuxTracer{reason: fmt.Sprintf("eBPF load failed (%v); using /proc fallback", err)}
	}
	return t
}

// loadFull embeds, verifies (CO-RE against node BTF) and attaches the probe.
// Any failure wraps context for the fallback reason string.
// INFRACITY_EBPF_DEBUG=1 turns on the kernel verifier log and appends its
// tail to the error — for diagnosing relocation failures on new kernels.
func loadFull() (*fullTracer, error) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(bpfObjects))
	if err != nil {
		return nil, fmt.Errorf("parse BPF objects: %w", err)
	}
	var opts ebpf.CollectionOptions
	if os.Getenv("INFRACITY_EBPF_DEBUG") == "1" {
		opts.Programs.LogLevel = ebpf.LogLevelInstruction | ebpf.LogLevelStats
		opts.Programs.LogSizeStart = 8 << 20
	}
	coll, err := ebpf.NewCollectionWithOptions(spec, opts)
	if err != nil {
		return nil, fmt.Errorf("load BPF objects into kernel (needs BTF + CAP_BPF): %w%s", err, verifierTail(err))
	}
	prog := coll.Programs["trace_tcp_state"]
	if prog == nil {
		coll.Close()
		return nil, fmt.Errorf("program trace_tcp_state not found in objects: %w", errMissingProg)
	}
	tp, err := link.Tracepoint("sock", "inet_sock_set_state", prog, nil)
	if err != nil {
		coll.Close()
		return nil, fmt.Errorf("attach sock/inet_sock_set_state: %w", err)
	}
	flows := coll.Maps["flows"]
	if flows == nil {
		_ = tp.Close()
		coll.Close()
		return nil, fmt.Errorf("map flows not found in objects: %w", errMissingProg)
	}
	t := &fullTracer{
		coll: coll, tp: tp, flows: flows,
		tcx: map[tcxKey]link.Link{},
	}
	t.reason = "eBPF tracepoint active (sock/inet_sock_set_state, consume-per-interval)"
	t.tcxNote = t.syncTCX()
	return t, nil
}

// syncTCX attaches TCX byte counters to every UP interface (best-effort) and
// detaches interfaces that disappeared (veth churn). Returns a reason suffix:
// empty when all attachments hold, otherwise the first failure. Callers must
// hold t.mu; it re-runs on every Sample so new interfaces join within one
// interval.
func (t *fullTracer) syncTCX() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Sprintf("; TCX bytes unavailable (list interfaces: %v)", err)
	}
	alive := map[int]bool{}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		alive[iface.Index] = true
		for _, dir := range []struct {
			ingress bool
			prog    string
			attach  ebpf.AttachType
		}{
			{false, "count_egress", ebpf.AttachTCXEgress},
			{true, "count_ingress", ebpf.AttachTCXIngress},
		} {
			key := tcxKey{ifindex: iface.Index, ingress: dir.ingress}
			if _, ok := t.tcx[key]; ok {
				continue
			}
			prog := t.coll.Programs[dir.prog]
			if prog == nil {
				return fmt.Sprintf("; TCX bytes unavailable (program %s missing: %v)", dir.prog, errMissingProg)
			}
			l, err := link.AttachTCX(link.TCXOptions{
				Interface: iface.Index,
				Program:   prog,
				Attach:    dir.attach,
			})
			if err != nil {
				return fmt.Sprintf("; TCX bytes unavailable on %s: %v", iface.Name, err)
			}
			t.tcx[key] = l
		}
	}
	for key, l := range t.tcx {
		if !alive[key.ifindex] {
			_ = l.Close()
			delete(t.tcx, key)
		}
	}
	if len(t.tcx) == 0 {
		return "; TCX bytes unavailable (no UP interfaces)"
	}
	return " + TCX byte accounting"
}

func (t *fullTracer) Enabled() bool { return true }
func (t *fullTracer) Reason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reason + t.tcxNote
}

// Sample drains the flows map (two-pass: collect then delete, so iteration
// is never mutated mid-walk) and converts entries to the shared Flow schema.
// It also resyncs TCX attachments so new interfaces join within one interval.
func (t *fullTracer) Sample() ([]netmon.Flow, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, fmt.Errorf("eBPF tracer closed: %w", errClosed)
	}
	t.tcxNote = t.syncTCX()
	var keys []FlowKey
	var vals []FlowVal
	it := t.flows.Iterate()
	var k FlowKey
	var v FlowVal
	for it.Next(&k, &v) {
		keys = append(keys, k)
		vals = append(vals, v)
	}
	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("iterate flows map: %w", err)
	}
	flows := make([]netmon.Flow, 0, len(keys))
	for i, key := range keys {
		if err := t.flows.Delete(key); err != nil {
			return nil, fmt.Errorf("consume flows map: %w", err)
		}
		f, ok := flowToNetmon(key, vals[i])
		if ok {
			flows = append(flows, f)
		}
	}
	return flows, nil
}

func (t *fullTracer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if err := t.tp.Close(); err != nil {
		t.coll.Close()
		return fmt.Errorf("detach tracepoint: %w", err)
	}
	for key, l := range t.tcx {
		_ = l.Close()
		delete(t.tcx, key)
	}
	t.coll.Close()
	return nil
}

var (
	errClosed      = errors.New("tracer closed")
	errMissingProg = errors.New("missing program or map")
)

// verifierTail appends the last lines of a cilium VerifierError (the summary
// error string omits most of the log). Capped so the fallback reason stays a
// log line, not a log flood.
func verifierTail(err error) string {
	var ve *ebpf.VerifierError
	if !errors.As(err, &ve) || len(ve.Log) == 0 {
		return ""
	}
	const maxLines = 25
	lines := ve.Log
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := "\nverifier:"
	for _, l := range lines {
		out += "\n" + strings.TrimRight(l, "\n")
		if len(out) > 3072 {
			break
		}
	}
	return out
}
