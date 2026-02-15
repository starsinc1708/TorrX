import type { TorrentRecord } from './types';

export const formatBytes = (bytes?: number): string => {
  if (bytes === undefined || bytes === null) return '\u2014';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, index);
  return `${value.toFixed(value >= 10 || index === 0 ? 0 : 1)} ${units[index]}`;
};

export const formatSpeed = (bytes?: number): string => {
  if (bytes === undefined || bytes === null) return '\u2014';
  return `${formatBytes(bytes)}/s`;
};

export const formatDate = (value?: string): string => {
  if (!value) return '\u2014';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
};

export const normalizeProgress = (record?: TorrentRecord): number => {
  if (!record) return 0;
  const total = record.totalBytes ?? 0;
  const done = record.doneBytes ?? 0;
  if (total <= 0) return 0;
  return Math.min(1, Math.max(0, done / total));
};

export const formatPercent = (value: number): string => `${(value * 100).toFixed(1)}%`;

export const extractExtension = (value: string): string => {
  const trimmed = value.trim();
  const index = trimmed.lastIndexOf('.');
  if (index === -1) return '';
  return trimmed.slice(index).toLowerCase();
};

export const playableExtensions = new Set(['.mp4', '.webm', '.ogg', '.m4v', '.mov']);
const videoExtensions = new Set(['.mp4', '.webm', '.ogg', '.m4v', '.mov', '.mkv', '.avi', '.wmv', '.flv', '.ts']);
const audioExtensions = new Set(['.mp3', '.aac', '.m4a', '.flac', '.wav', '.wma', '.opus', '.oga', '.ogg']);
const imageExtensions = new Set(['.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp', '.svg', '.tiff', '.avif']);

export const isVideoFile = (path: string): boolean => {
  const ext = extractExtension(path);
  return videoExtensions.has(ext);
};

export const isAudioFile = (path: string): boolean => {
  const ext = extractExtension(path);
  return audioExtensions.has(ext);
};

export const isImageFile = (path: string): boolean => {
  const ext = extractExtension(path);
  return imageExtensions.has(ext);
};

/**
 * Decode a base64-encoded piece bitfield into an array of booleans.
 * Each bit in the bitfield represents one piece (MSB first within each byte).
 */
export const decodePieceBitfield = (encoded: string, numPieces: number): boolean[] => {
  if (!encoded || numPieces <= 0) return [];
  const binary = atob(encoded);
  const pieces = new Array<boolean>(numPieces);
  for (let i = 0; i < numPieces; i++) {
    const byteIndex = i >> 3;
    const bitIndex = 7 - (i & 7);
    pieces[i] = byteIndex < binary.length ? ((binary.charCodeAt(byteIndex) >> bitIndex) & 1) === 1 : false;
  }
  return pieces;
};

/**
 * Bucket pieces into segments, returning fill ratio (0..1) for each segment.
 */
export const bucketPieces = (pieces: boolean[], bucketCount: number): number[] => {
  if (pieces.length === 0 || bucketCount <= 0) return [];
  const count = Math.min(bucketCount, pieces.length);
  const result = new Array<number>(count);
  for (let b = 0; b < count; b++) {
    const start = Math.floor((b * pieces.length) / count);
    const end = Math.floor(((b + 1) * pieces.length) / count);
    let completed = 0;
    for (let i = start; i < end; i++) {
      if (pieces[i]) completed++;
    }
    result[b] = end > start ? completed / (end - start) : 0;
  }
  return result;
};

export const formatTime = (seconds: number): string => {
  if (!isFinite(seconds) || seconds < 0) return '0:00';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  const pad = (n: number) => n.toString().padStart(2, '0');
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
};
