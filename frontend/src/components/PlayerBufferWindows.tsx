import React, { useMemo } from 'react';

import type { HLSSettings } from '../types';
import { cn } from '../lib/cn';

type BufferedRange = { start: number; end: number };

type PlayerBufferWindowsProps = {
  settings: HLSSettings | null;
  selectedFileLengthBytes: number;
  selectedFileCompletedBytes: number;
  mediaDurationSec: number;
  currentTimeSec: number;
  bufferedRanges: BufferedRange[];
  className?: string;
};

const MB = 1024 * 1024;
const CHUNK_COUNT = 24;

type BufferMetric = {
  label: string;
  valueMB: number;
  targetMB: number;
  ratio: number;
  toneClass: string;
};

const clamp = (value: number, min = 0, max = 1) => Math.max(min, Math.min(max, value));

const sumBufferedInRange = (ranges: BufferedRange[], windowStart: number, windowEnd: number) => {
  if (windowEnd <= windowStart) return 0;
  let total = 0;
  for (const range of ranges) {
    const overlapStart = Math.max(windowStart, range.start);
    const overlapEnd = Math.min(windowEnd, range.end);
    if (overlapEnd > overlapStart) total += overlapEnd - overlapStart;
  }
  return total;
};

const BufferRow: React.FC<{ metric: BufferMetric }> = ({ metric }) => {
  const filledChunks = Math.floor(clamp(metric.ratio) * CHUNK_COUNT);
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="font-medium text-foreground">{metric.label}</span>
        <span className="tabular-nums text-muted-foreground">
          {metric.valueMB.toFixed(1)} / {metric.targetMB.toFixed(1)} MB
        </span>
      </div>
      <div
        className="grid gap-1"
        style={{ gridTemplateColumns: `repeat(${CHUNK_COUNT}, minmax(0, 1fr))` }}
      >
        {Array.from({ length: CHUNK_COUNT }).map((_, idx) => (
          <span
            key={`${metric.label}-${idx}`}
            className={cn(
              'h-2 rounded-[3px] border border-border/50 bg-muted/30 transition-colors',
              idx < filledChunks ? metric.toneClass : '',
            )}
          />
        ))}
      </div>
    </div>
  );
};

const PlayerBufferWindows: React.FC<PlayerBufferWindowsProps> = ({
  settings,
  selectedFileLengthBytes,
  selectedFileCompletedBytes,
  mediaDurationSec,
  currentTimeSec,
  bufferedRanges,
  className,
}) => {
  const metrics = useMemo(() => {
    if (!settings) return [] as BufferMetric[];

    const prebufferTargetMB = Math.max(0, settings.prebufferMB || 0);
    const beforeTargetMB = Math.max(0, settings.windowBeforeMB || 0);
    const afterTargetMB = Math.max(0, settings.windowAfterMB || 0);
    const fileLength = Math.max(0, selectedFileLengthBytes);
    const fileCompleted = Math.max(0, selectedFileCompletedBytes);
    const bytesPerSecond = mediaDurationSec > 0 && fileLength > 0 ? fileLength / mediaDurationSec : 0;

    const prebufferValueMB = Math.min(fileCompleted / MB, prebufferTargetMB);
    const prebufferRatio = prebufferTargetMB > 0 ? prebufferValueMB / prebufferTargetMB : 0;

    let beforeValueMB = 0;
    let afterValueMB = 0;
    if (bytesPerSecond > 0) {
      const beforeTargetSec = (beforeTargetMB * MB) / bytesPerSecond;
      const afterTargetSec = (afterTargetMB * MB) / bytesPerSecond;

      if (beforeTargetSec > 0) {
        const bufferedBeforeSec = sumBufferedInRange(
          bufferedRanges,
          Math.max(0, currentTimeSec - beforeTargetSec),
          currentTimeSec,
        );
        beforeValueMB = (bufferedBeforeSec * bytesPerSecond) / MB;
      }

      if (afterTargetSec > 0) {
        const bufferedAfterSec = sumBufferedInRange(
          bufferedRanges,
          currentTimeSec,
          currentTimeSec + afterTargetSec,
        );
        afterValueMB = (bufferedAfterSec * bytesPerSecond) / MB;
      }
    }

    return [
      {
        label: 'Prebuffer',
        valueMB: prebufferValueMB,
        targetMB: prebufferTargetMB,
        ratio: prebufferTargetMB > 0 ? prebufferValueMB / prebufferTargetMB : 0,
        toneClass: 'border-sky-400/60 bg-sky-500/80',
      },
      {
        label: 'Window before',
        valueMB: Math.min(beforeTargetMB, beforeValueMB),
        targetMB: beforeTargetMB,
        ratio: beforeTargetMB > 0 ? beforeValueMB / beforeTargetMB : 0,
        toneClass: 'border-emerald-400/60 bg-emerald-500/80',
      },
      {
        label: 'Window after',
        valueMB: Math.min(afterTargetMB, afterValueMB),
        targetMB: afterTargetMB,
        ratio: afterTargetMB > 0 ? afterValueMB / afterTargetMB : 0,
        toneClass: 'border-amber-400/60 bg-amber-500/80',
      },
    ] as BufferMetric[];
  }, [
    settings,
    selectedFileLengthBytes,
    selectedFileCompletedBytes,
    mediaDurationSec,
    currentTimeSec,
    bufferedRanges,
  ]);

  if (!settings || metrics.length === 0) return null;

  return (
    <div className={cn('rounded-xl border border-border/70 bg-card/85 p-3 backdrop-blur-md', className)}>
      <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
        Stream buffer
      </div>
      <div className="space-y-3">
        {metrics.map((metric) => (
          <BufferRow key={metric.label} metric={metric} />
        ))}
      </div>
    </div>
  );
};

export default PlayerBufferWindows;
