import React, { useEffect, useMemo, useRef } from 'react';
import { decodePieceBitfield, bucketPieces } from '../utils';
import { cn } from '../lib/cn';

interface PieceBarProps {
  numPieces?: number;
  pieceBitfield?: string;
  height?: number;
  className?: string;
}

const SEGMENT_WIDTH = 3;
const GAP = 1;

const PieceBar: React.FC<PieceBarProps> = ({ numPieces, pieceBitfield, height = 14, className }) => {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const pieces = useMemo(
    () => (numPieces && pieceBitfield ? decodePieceBitfield(pieceBitfield, numPieces) : []),
    [numPieces, pieceBitfield],
  );

  useEffect(() => {
    const canvas = canvasRef.current;
    const container = containerRef.current;
    if (!canvas || !container || pieces.length === 0) return;

    const rect = container.getBoundingClientRect();
    const width = Math.floor(rect.width);
    if (width <= 0) return;

    const dpr = window.devicePixelRatio || 1;
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    canvas.style.width = `${width}px`;
    canvas.style.height = `${height}px`;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.scale(dpr, dpr);

    const bucketCount = Math.max(1, Math.floor(width / (SEGMENT_WIDTH + GAP)));
    const buckets = bucketPieces(pieces, bucketCount);

    ctx.clearRect(0, 0, width, height);

    for (let i = 0; i < buckets.length; i++) {
      const x = i * (SEGMENT_WIDTH + GAP);
      const fill = buckets[i];

      if (fill >= 1) {
        ctx.fillStyle = '#22c55e';
      } else if (fill > 0) {
        ctx.fillStyle = `rgba(34, 197, 94, ${0.3 + fill * 0.7})`;
      } else {
        ctx.fillStyle = 'rgba(255, 255, 255, 0.06)';
      }

      ctx.fillRect(x, 0, SEGMENT_WIDTH, height);
    }
  }, [pieces, height]);

  // Re-render on resize.
  useEffect(() => {
    const container = containerRef.current;
    if (!container || pieces.length === 0) return;

    const observer = new ResizeObserver(() => {
      // Trigger re-render by dispatching a custom re-draw.
      const canvas = canvasRef.current;
      if (!canvas) return;
      const rect = container.getBoundingClientRect();
      const width = Math.floor(rect.width);
      if (width <= 0) return;

      const dpr = window.devicePixelRatio || 1;
      canvas.width = width * dpr;
      canvas.height = height * dpr;
      canvas.style.width = `${width}px`;
      canvas.style.height = `${height}px`;

      const ctx = canvas.getContext('2d');
      if (!ctx) return;
      ctx.scale(dpr, dpr);

      const bucketCount = Math.max(1, Math.floor(width / (SEGMENT_WIDTH + GAP)));
      const buckets = bucketPieces(pieces, bucketCount);

      ctx.clearRect(0, 0, width, height);

      for (let i = 0; i < buckets.length; i++) {
        const x = i * (SEGMENT_WIDTH + GAP);
        const fill = buckets[i];

        if (fill >= 1) {
          ctx.fillStyle = '#22c55e';
        } else if (fill > 0) {
          ctx.fillStyle = `rgba(34, 197, 94, ${0.3 + fill * 0.7})`;
        } else {
          ctx.fillStyle = 'rgba(255, 255, 255, 0.06)';
        }

        ctx.fillRect(x, 0, SEGMENT_WIDTH, height);
      }
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, [pieces, height]);

  if (!numPieces || !pieceBitfield) return null;

  return (
    <div
      ref={containerRef}
      className={cn(
        'w-full overflow-hidden rounded-md border border-border/70 bg-muted/30',
        className,
      )}
    >
      <canvas ref={canvasRef} />
    </div>
  );
};

export default PieceBar;
