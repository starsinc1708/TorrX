import { describe, it, expect, beforeEach, vi } from 'vitest';
import { getTorrentPlayerPreferences, patchTorrentPlayerPreferences } from './playerPreferences';

describe('playerPreferences', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.useFakeTimers({ now: new Date('2026-02-20T12:00:00Z') });
  });

  describe('quality switching persistence', () => {
    it('stores preferred quality level for a torrent', () => {
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: 2 });

      const prefs = getTorrentPlayerPreferences('torrent1');
      expect(prefs).not.toBeNull();
      expect(prefs!.preferredQualityLevel).toBe(2);
    });

    it('switches quality from 480p to 1080p (level 0 → level 2)', () => {
      // User starts at level 0 (480p)
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: 0 });
      expect(getTorrentPlayerPreferences('torrent1')!.preferredQualityLevel).toBe(0);

      // User switches to level 2 (1080p)
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: 2 });
      expect(getTorrentPlayerPreferences('torrent1')!.preferredQualityLevel).toBe(2);
    });

    it('persists quality level across reads (simulating session reload)', () => {
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: 1 });

      // Simulate "closing and reopening" by reading fresh from localStorage
      const prefs = getTorrentPlayerPreferences('torrent1');
      expect(prefs!.preferredQualityLevel).toBe(1);
    });

    it('supports auto quality level (-1)', () => {
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: -1 });
      expect(getTorrentPlayerPreferences('torrent1')!.preferredQualityLevel).toBe(-1);
    });

    it('rejects invalid quality level (negative below -1)', () => {
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: -2 });

      const prefs = getTorrentPlayerPreferences('torrent1');
      // -2 is invalid, so preferredQualityLevel should not be set
      expect(prefs?.preferredQualityLevel).toBeUndefined();
    });

    it('preserves quality level when updating other preferences', () => {
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: 2 });
      patchTorrentPlayerPreferences('torrent1', { audioTrack: 1 });
      patchTorrentPlayerPreferences('torrent1', { subtitleTrack: 0 });

      const prefs = getTorrentPlayerPreferences('torrent1');
      expect(prefs!.preferredQualityLevel).toBe(2);
      expect(prefs!.audioTrack).toBe(1);
      expect(prefs!.subtitleTrack).toBe(0);
    });

    it('maintains independent quality levels per torrent', () => {
      patchTorrentPlayerPreferences('torrentA', { preferredQualityLevel: 0 });
      patchTorrentPlayerPreferences('torrentB', { preferredQualityLevel: 2 });

      expect(getTorrentPlayerPreferences('torrentA')!.preferredQualityLevel).toBe(0);
      expect(getTorrentPlayerPreferences('torrentB')!.preferredQualityLevel).toBe(2);
    });
  });

  describe('watch history resume preferences', () => {
    it('stores and retrieves audio/subtitle track preferences for resume', () => {
      patchTorrentPlayerPreferences('torrent1', {
        audioTrack: 2,
        subtitleTrack: 1,
        playbackRate: 1.5,
      });

      const prefs = getTorrentPlayerPreferences('torrent1');
      expect(prefs!.audioTrack).toBe(2);
      expect(prefs!.subtitleTrack).toBe(1);
      expect(prefs!.playbackRate).toBe(1.5);
    });

    it('preserves all preferences across quality switches', () => {
      // Set up initial preferences as if user was watching
      patchTorrentPlayerPreferences('torrent1', {
        audioTrack: 1,
        subtitleTrack: 0,
        playbackRate: 1.25,
        preferredQualityLevel: 0,
      });

      // Switch quality (simulates quality change during playback)
      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: 2 });

      // All other preferences should be preserved
      const prefs = getTorrentPlayerPreferences('torrent1');
      expect(prefs!.audioTrack).toBe(1);
      expect(prefs!.subtitleTrack).toBe(0);
      expect(prefs!.playbackRate).toBe(1.25);
      expect(prefs!.preferredQualityLevel).toBe(2);
    });

    it('clamps playback rate within valid range', () => {
      patchTorrentPlayerPreferences('torrent1', { playbackRate: 5 });
      expect(getTorrentPlayerPreferences('torrent1')!.playbackRate).toBe(2);

      patchTorrentPlayerPreferences('torrent1', { playbackRate: 0.1 });
      expect(getTorrentPlayerPreferences('torrent1')!.playbackRate).toBe(0.25);
    });

    it('returns null for unknown torrent (no resume data)', () => {
      expect(getTorrentPlayerPreferences('unknown')).toBeNull();
    });

    it('handles empty torrent ID gracefully', () => {
      patchTorrentPlayerPreferences('', { preferredQualityLevel: 1 });
      expect(getTorrentPlayerPreferences('')).toBeNull();
    });
  });

  describe('localStorage resilience', () => {
    it('handles corrupted localStorage data', () => {
      localStorage.setItem('playerPreferencesByTorrent:v1', 'not-json');
      expect(getTorrentPlayerPreferences('torrent1')).toBeNull();
    });

    it('handles malformed preference entries', () => {
      localStorage.setItem(
        'playerPreferencesByTorrent:v1',
        JSON.stringify({ torrent1: 'not-an-object' }),
      );
      expect(getTorrentPlayerPreferences('torrent1')).toBeNull();
    });

    it('recovers after corrupted data by writing fresh', () => {
      localStorage.setItem('playerPreferencesByTorrent:v1', 'broken');

      patchTorrentPlayerPreferences('torrent1', { preferredQualityLevel: 1 });

      const prefs = getTorrentPlayerPreferences('torrent1');
      expect(prefs!.preferredQualityLevel).toBe(1);
    });
  });
});
