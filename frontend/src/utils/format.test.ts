// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from 'vitest';
import { esc, fmtK, shortId } from './format';

describe('fmtK', () => {
  it('passes small numbers through', () => {
    expect(fmtK(0)).toBe('0');
    expect(fmtK(68)).toBe('68');
    expect(fmtK(999)).toBe('999');
  });
  it('compacts thousands', () => {
    expect(fmtK(1000)).toBe('1.0k');
    expect(fmtK(2400)).toBe('2.4k');
    expect(fmtK(28062)).toBe('28.1k');
  });
});

describe('shortId', () => {
  it('takes the last segment', () => {
    expect(shortId('service/production/payments/api')).toBe('api');
    expect(shortId('external/production/10.96.0.1')).toBe('10.96.0.1');
  });
  it('handles bare names', () => {
    expect(shortId('frontend')).toBe('frontend');
  });
});

describe('esc', () => {
  it('escapes HTML metacharacters', () => {
    expect(esc('<b>&"')).toBe('&lt;b&gt;&amp;&quot;');
  });
  it('leaves plain text alone', () => {
    expect(esc('api-7f8d9')).toBe('api-7f8d9');
  });
});
