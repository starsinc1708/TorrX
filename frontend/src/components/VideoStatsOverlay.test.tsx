// frontend/src/components/VideoStatsOverlay.test.tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { VideoStatsOverlay } from './VideoStatsOverlay';
import type Hls from 'hls.js';
import React from 'react';

const mockHlsRef = { current: null } as React.RefObject<Hls | null>;
const mockVideoRef = { current: null } as React.RefObject<HTMLVideoElement | null>;

describe('VideoStatsOverlay', () => {
  it('renders nothing when visible=false', () => {
    const { container } = render(
      <VideoStatsOverlay hlsRef={mockHlsRef} videoRef={mockVideoRef} visible={false} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders panel with heading when visible=true', () => {
    render(
      <VideoStatsOverlay hlsRef={mockHlsRef} videoRef={mockVideoRef} visible={true} />,
    );
    expect(screen.getByTestId('stats-overlay')).toBeInTheDocument();
    expect(screen.getByText('Video Stats')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', () => {
    const onClose = vi.fn();
    const { getAllByRole } = render(
      <VideoStatsOverlay
        hlsRef={mockHlsRef}
        videoRef={mockVideoRef}
        visible={true}
        onClose={onClose}
      />,
    );
    getAllByRole('button')[0].click();
    expect(onClose).toHaveBeenCalledOnce();
  });
});
