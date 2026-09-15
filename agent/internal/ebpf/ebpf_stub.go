// SPDX-License-Identifier: Apache-2.0
//go:build !linux

package ebpf

import "github.com/infracity/infracity/agent/internal/netmon"

type stubTracer struct{}

func newTracer() Tracer { return &stubTracer{} }

func (s *stubTracer) Enabled() bool { return false }
func (s *stubTracer) Reason() string {
	return "eBPF requires linux + CAP_BPF/CAP_NET_ADMIN; using /proc fallback"
}
func (s *stubTracer) Sample() ([]netmon.Flow, error) { return netmon.SampleProcNet() }
func (s *stubTracer) Close() error                   { return nil }
