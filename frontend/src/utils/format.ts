// SPDX-License-Identifier: Apache-2.0
// Pure display helpers shared by the city renderer and command center.
// Kept dependency-free so they are trivially unit-testable (see format.test.ts).

/** Compact number: 950 -> "950", 2400 -> "2.4k". */
export const fmtK = (v: number): string =>
  v >= 1000 ? (v / 1000).toFixed(1) + 'k' : v.toFixed(0);

/** Last path segment of a canonical node id. */
export function shortId(id: string): string {
  const i = id.lastIndexOf('/');
  return i >= 0 ? id.slice(i + 1) : id;
}

/** Minimal HTML escaper for tooltip/chip innerHTML. */
export function esc(s: string): string {
  return s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]!));
}
