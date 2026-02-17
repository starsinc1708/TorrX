import { useCallback, useState } from 'react';

export function useScreenshot(videoRef: React.RefObject<HTMLVideoElement | null>): {
  screenshotFlash: boolean;
  takeScreenshot: () => Promise<void>;
} {
  const [screenshotFlash, setScreenshotFlash] = useState(false);

  const takeScreenshot = useCallback(async () => {
    const video = videoRef.current;
    if (!video) return;
    const canvas = document.createElement('canvas');
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);

    try {
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'));
      if (blob) {
        await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
      }
    } catch {
      const link = document.createElement('a');
      link.href = canvas.toDataURL('image/png');
      link.download = `screenshot-${Date.now()}.png`;
      link.click();
    }

    setScreenshotFlash(true);
    setTimeout(() => setScreenshotFlash(false), 400);
  }, [videoRef]);

  return { screenshotFlash, takeScreenshot };
}
