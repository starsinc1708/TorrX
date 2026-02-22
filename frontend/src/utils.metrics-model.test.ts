import { describe, expect, it } from 'vitest';
import type { FileRef, SessionState, TorrentRecord } from './types';
import {
  countFilePriorities,
  getFileProgress,
  getTorrentProgress,
  normalizeFilePriority,
} from './utils';

describe('metrics model helpers', () => {
  it('reads torrent progress only from backend progress fields', () => {
    const record = {
      id: 't1',
      status: 'active',
      doneBytes: 1000,
      totalBytes: 1000,
    } as TorrentRecord;
    const state = { id: 't1', progress: 0.42 } as SessionState;

    expect(getTorrentProgress(null, record)).toBe(0);
    expect(getTorrentProgress(state, record)).toBeCloseTo(0.42, 6);
  });

  it('reads file progress only from backend file progress', () => {
    const noBackendProgress = {
      index: 0,
      path: 'movie.mkv',
      length: 1000,
      bytesCompleted: 1000,
    } as FileRef;
    const backendProgress = {
      index: 0,
      path: 'movie.mkv',
      length: 1000,
      bytesCompleted: 300,
      progress: 0.3,
    } as FileRef;

    expect(getFileProgress(noBackendProgress)).toBe(0);
    expect(getFileProgress(noBackendProgress, backendProgress)).toBeCloseTo(0.3, 6);
  });

  it('normalizes and counts file priorities with backend vocabulary', () => {
    expect(normalizeFilePriority('UNKNOWN')).toBe('normal');
    expect(normalizeFilePriority('now')).toBe('now');

    const counts = countFilePriorities([
      { index: 0, path: 'a', length: 1, priority: 'none' } as FileRef,
      { index: 1, path: 'b', length: 1, priority: 'low' } as FileRef,
      { index: 2, path: 'c', length: 1, priority: 'normal' } as FileRef,
      { index: 3, path: 'd', length: 1, priority: 'high' } as FileRef,
      { index: 4, path: 'e', length: 1, priority: 'now' } as FileRef,
      { index: 5, path: 'f', length: 1, priority: 'unexpected' as never } as FileRef,
    ]);

    expect(counts.none).toBe(1);
    expect(counts.low).toBe(1);
    expect(counts.normal).toBe(2);
    expect(counts.high).toBe(1);
    expect(counts.now).toBe(1);
  });
});
