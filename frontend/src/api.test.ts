import { describe, it, expect } from 'vitest';
import { buildUrl } from './api';

describe('buildUrl', () => {
  it('returns path as-is when no API_BASE is set', () => {
    expect(buildUrl('/torrents')).toBe('/torrents');
  });

  it('returns path with leading slash', () => {
    expect(buildUrl('/search')).toBe('/search');
  });

  it('handles settings paths', () => {
    expect(buildUrl('/settings/storage')).toBe('/settings/storage');
  });
});
