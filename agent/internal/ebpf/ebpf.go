// SPDX-License-Identifier: Apache-2.0
// Package ebpf is the Go loader interface for InfraCity's eBPF probes.
//
// C sources live in ebpf/ (sock-trace.bpf.c). This package exposes a stable Go
// API with graceful degradation: if the kernel, privileges or object files are
// unavailable, Enabled() returns false and the agent falls back to
// /proc/net/tcp + Kubernetes metadata (see agent/internal/netmon).
//
// Production wiring uses github.com/cilium/ebpf (see ebpf/README.md). To keep
// the default build portable (no libbpf/clang dependency, darwin-friendly),
// the loader is split behind a build tag: ebpf_linux.go enables the real
// loader, ebpf_stub.go is used everywhere else.
package ebpf

import "github.com/infracity/infracity/agent/internal/netmon"

// Tracer is the minimal interface the agent loop depends on.
type Tracer interface {
	Enabled() bool
	Reason() string
	Sample() ([]netmon.Flow, error)
	Close() error
}

// New returns the platform tracer (real on linux with privileges, stub elsewhere).
func New() Tracer { return newTracer() }
