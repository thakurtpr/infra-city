// SPDX-License-Identifier: Apache-2.0
//go:build linux && ebpf_full

package ebpf

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
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
type fullTracer struct {
	mu     sync.Mutex
	coll   *ebpf.Collection
	tp     link.Link
	flows  *ebpf.Map
	reason string
	closed bool
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
		return nil, fmt.Errorf("load BPF objects into kernel (needs BTF + CAP_BPF): %w", err)
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
	return &fullTracer{
		coll: coll, tp: tp, flows: flows,
		reason: "eBPF tracepoint active (sock/inet_sock_set_state, consume-per-interval)",
	}, nil
}

func (t *fullTracer) Enabled() bool { return true }
func (t *fullTracer) Reason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reason
}

// Sample drains the flows map (two-pass: collect then delete, so iteration
// is never mutated mid-walk) and converts entries to the shared Flow schema.
func (t *fullTracer) Sample() ([]netmon.Flow, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, fmt.Errorf("eBPF tracer closed: %w", errClosed)
	}
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
	t.coll.Close()
	return nil
}

var (
	errClosed      = errors.New("tracer closed")
	errMissingProg = errors.New("missing program or map")
)
