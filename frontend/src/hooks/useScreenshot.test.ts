import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useScreenshot } from './useScreenshot';

describe('useScreenshot', () => {
  let mockCanvas: {
    width: number;
    height: number;
    getContext: ReturnType<typeof vi.fn>;
    toBlob: ReturnType<typeof vi.fn>;
    toDataURL: ReturnType<typeof vi.fn>;
  };
  let mockCtx: { drawImage: ReturnType<typeof vi.fn> };

  beforeEach(() => {
    vi.useFakeTimers();
    mockCtx = { drawImage: vi.fn() };
    mockCanvas = {
      width: 0,
      height: 0,
      getContext: vi.fn(() => mockCtx),
      toBlob: vi.fn(),
      toDataURL: vi.fn(() => 'data:image/png;base64,fake'),
    };
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
      if (tag === 'canvas') return mockCanvas as any;
      // For 'a' elements (download fallback), return a real element
      return document.createElementNS('http://www.w3.org/1999/xhtml', tag) as any;
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('screenshotFlash starts false', () => {
    const ref = { current: null };
    const { result } = renderHook(() => useScreenshot(ref));
    expect(result.current.screenshotFlash).toBe(false);
  });

  it('screenshotFlash goes true then false after taking screenshot', async () => {
    const video = {
      videoWidth: 1920,
      videoHeight: 1080,
    } as HTMLVideoElement;
    const ref = { current: video };

    const blob = new Blob(['test'], { type: 'image/png' });
    mockCanvas.toBlob.mockImplementation((cb: (b: Blob) => void) => cb(blob));

    // Mock clipboard API
    const writeMock = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, {
      clipboard: { write: writeMock },
    });
    // ClipboardItem constructor mock
    vi.stubGlobal(
      'ClipboardItem',
      class {
        constructor(public items: Record<string, Blob>) {}
      },
    );

    const { result } = renderHook(() => useScreenshot(ref));

    await act(async () => {
      await result.current.takeScreenshot();
    });

    expect(result.current.screenshotFlash).toBe(true);

    act(() => {
      vi.advanceTimersByTime(400);
    });

    expect(result.current.screenshotFlash).toBe(false);
  });

  it('falls back to download when clipboard write fails', async () => {
    const video = {
      videoWidth: 1280,
      videoHeight: 720,
    } as HTMLVideoElement;
    const ref = { current: video };

    const blob = new Blob(['test'], { type: 'image/png' });
    mockCanvas.toBlob.mockImplementation((cb: (b: Blob) => void) => cb(blob));

    // Make clipboard.write reject
    Object.assign(navigator, {
      clipboard: { write: vi.fn().mockRejectedValue(new Error('denied')) },
    });
    vi.stubGlobal(
      'ClipboardItem',
      class {
        constructor(public items: Record<string, Blob>) {}
      },
    );

    // Track the created <a> link element
    let clickedLink: HTMLAnchorElement | null = null;
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
      if (tag === 'canvas') return mockCanvas as any;
      if (tag === 'a') {
        const link = document.createElementNS('http://www.w3.org/1999/xhtml', 'a') as HTMLAnchorElement;
        link.click = vi.fn();
        clickedLink = link;
        return link;
      }
      return document.createElementNS('http://www.w3.org/1999/xhtml', tag) as any;
    });

    const { result } = renderHook(() => useScreenshot(ref));

    await act(async () => {
      await result.current.takeScreenshot();
    });

    expect(clickedLink).not.toBeNull();
    expect(clickedLink!.download).toMatch(/^screenshot-\d+\.png$/);
    expect(clickedLink!.click).toHaveBeenCalled();

    // Flash still fires even on fallback
    expect(result.current.screenshotFlash).toBe(true);
  });

  it('does nothing when video ref is null', async () => {
    const ref = { current: null };
    const { result } = renderHook(() => useScreenshot(ref));

    await act(async () => {
      await result.current.takeScreenshot();
    });

    expect(result.current.screenshotFlash).toBe(false);
    expect(mockCanvas.getContext).not.toHaveBeenCalled();
  });
});
