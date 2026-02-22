import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import PlayerFilesPanel from './PlayerFilesPanel';
import type { FileRef } from '../types';

const makeFiles = (overrides?: Partial<FileRef>[]): FileRef[] => {
  const base: FileRef[] = [
    { index: 0, path: 'episode-01.mkv', length: 1000, progress: 1, priority: 'now' },
    { index: 1, path: 'episode-02.mkv', length: 1000, progress: 0.2, priority: 'none' },
    { index: 2, path: 'episode-03.mkv', length: 1000, progress: 0.1, priority: 'low' },
  ];
  if (!overrides || overrides.length === 0) return base;
  return base.map((file, idx) => ({ ...file, ...(overrides[idx] ?? {}) }));
};

describe('PlayerFilesPanel', () => {
  it('shows effective priority status for selected file and neighbors', () => {
    render(
      <PlayerFilesPanel
        files={makeFiles()}
        selectedFileIndex={0}
        sessionState={null}
        prioritizeActiveFileOnly={true}
        onSelectFile={vi.fn()}
      />,
    );

    expect(screen.getByText('Focus priority mode')).toBeInTheDocument();
    expect(screen.getByText('Selected file priority:')).toBeInTheDocument();
    expect(screen.getByText('n:none 1')).toBeInTheDocument();
    expect(screen.getByText('n:low 1')).toBeInTheDocument();
  });

  it('shows mismatch warning when focus mode is enabled but neighbors are above low', () => {
    render(
      <PlayerFilesPanel
        files={makeFiles([{ priority: 'now' }, { priority: 'normal' }, { priority: 'low' }])}
        selectedFileIndex={0}
        sessionState={null}
        prioritizeActiveFileOnly={true}
        onSelectFile={vi.fn()}
      />,
    );

    expect(screen.getByText(/Focus mode expects neighbors in `none`/)).toBeInTheDocument();
  });
});
