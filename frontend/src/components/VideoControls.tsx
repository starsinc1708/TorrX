import React from 'react';
import {
  Pause,
  Play,
  ChevronLeft,
  ChevronRight,
  SkipBack,
  SkipForward,
  Volume2,
  VolumeX,
  Settings2,
  Check,
  Camera,
  Maximize2,
  Minimize2,
  Info,
} from 'lucide-react';
import { cn } from '../lib/cn';
import { formatTime } from '../utils';
import type { MediaTrack } from '../types';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from './ui/dropdown-menu';

interface VideoControlsProps {
  ctrlBtnClassName: string;
  playing: boolean;
  togglePlay: () => void;
  selectAdjacentFile: (direction: 'prev' | 'next', wrap: boolean) => void;
  prevFile: boolean;
  nextFile: boolean;
  skip: (seconds: number) => void;
  toggleMute: () => void;
  muted: boolean;
  volume: number;
  handleVolumeChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  displayCurrentTime: number;
  displayDuration: number;
  settingsOpen: boolean;
  setSettingsOpen: (open: boolean) => void;
  dropdownPortalContainer: HTMLElement | null;
  audioTracks: MediaTrack[];
  selectedAudioTrack: number | null;
  onSelectAudioTrack: (index: number) => void;
  trackLabel: (track: MediaTrack) => string;
  subtitlesReady: boolean;
  subtitleTracks: MediaTrack[];
  selectedSubtitleTrack: number | null;
  onSelectSubtitleTrack: (index: number | null) => void;
  speedMenuOpen: boolean;
  setSpeedMenuOpen: (open: boolean) => void;
  playbackRate: number;
  videoRef: React.RefObject<HTMLVideoElement>;
  handleOpenInfo?: () => void;
  torrentId: string | null;
  takeScreenshot: () => void;
  toggleFullscreen: () => void;
  isFullscreen: boolean;
  streamUrl: string;
}

export const VideoControls: React.FC<VideoControlsProps> = ({
  ctrlBtnClassName,
  playing,
  togglePlay,
  selectAdjacentFile,
  prevFile,
  nextFile,
  skip,
  toggleMute,
  muted,
  volume,
  handleVolumeChange,
  displayCurrentTime,
  displayDuration,
  settingsOpen,
  setSettingsOpen,
  dropdownPortalContainer,
  audioTracks,
  selectedAudioTrack,
  onSelectAudioTrack,
  trackLabel,
  subtitlesReady,
  subtitleTracks,
  selectedSubtitleTrack,
  onSelectSubtitleTrack,
  speedMenuOpen,
  setSpeedMenuOpen,
  playbackRate,
  videoRef,
  handleOpenInfo,
  torrentId,
  takeScreenshot,
  toggleFullscreen,
  isFullscreen,
  streamUrl,
}) => {

  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex min-w-0 items-center gap-1">
        <button className={ctrlBtnClassName} onClick={togglePlay} title={playing ? 'Pause' : 'Play'}>
          {playing ? <Pause size={20} /> : <Play size={20} />}
        </button>
        <button
          className={ctrlBtnClassName}
          onClick={() => selectAdjacentFile('prev', false)}
          disabled={!prevFile}
          title="Previous file"
        >
          <ChevronLeft size={18} />
        </button>
        <button
          className={ctrlBtnClassName}
          onClick={() => selectAdjacentFile('next', false)}
          disabled={!nextFile}
          title="Next file"
        >
          <ChevronRight size={18} />
        </button>
        <button className={ctrlBtnClassName} onClick={() => skip(-10)} title="Rewind 10s">
          <SkipBack size={18} />
          <span className="pointer-events-none absolute bottom-1 right-1 text-[9px] font-bold leading-none text-white/80">
            10
          </span>
        </button>
        <button className={ctrlBtnClassName} onClick={() => skip(10)} title="Forward 10s">
          <SkipForward size={18} />
          <span className="pointer-events-none absolute bottom-1 right-1 text-[9px] font-bold leading-none text-white/80">
            10
          </span>
        </button>

        <div className="group flex items-center">
          <button className={ctrlBtnClassName} onClick={toggleMute} title={muted ? 'Unmute' : 'Mute'}>
            {muted || volume === 0 ? <VolumeX size={18} /> : <Volume2 size={18} />}
          </button>
          <input
            type="range"
            className={cn(
              'ctrl-volume-slider ml-1 w-0 opacity-0 ' +
                'transition-[width,opacity,margin] duration-200 ease-out ' +
                'group-hover:w-[70px] group-hover:opacity-100',
            )}
            min="0"
            max="1"
            step="0.05"
            value={muted ? 0 : volume}
            onChange={handleVolumeChange}
          />
        </div>

        <span className="hidden whitespace-nowrap px-2 text-xs font-medium tabular-nums text-white/70 sm:inline">
          {formatTime(displayCurrentTime)} / {formatTime(displayDuration)}
        </span>
      </div>

      <div className="flex flex-shrink-0 items-center gap-1">
        <DropdownMenu open={settingsOpen} onOpenChange={setSettingsOpen}>
          <DropdownMenuTrigger asChild>
            <button className={ctrlBtnClassName} title="Audio / subtitles" aria-label="Audio / subtitles">
              <Settings2 size={18} />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            portalContainer={dropdownPortalContainer}
            className="w-[min(92vw,360px)] sm:min-w-[320px] max-h-[min(70dvh,560px)] overflow-y-auto overscroll-contain"
          >
            <DropdownMenuLabel>Audio</DropdownMenuLabel>
            <DropdownMenuGroup>
              {audioTracks.length === 0 ? (
                <DropdownMenuItem disabled>No audio tracks</DropdownMenuItem>
              ) : (
                audioTracks.map((track, idx) => {
                  const active =
                    selectedAudioTrack === track.index || (selectedAudioTrack === null && idx === 0);
                  return (
                    <DropdownMenuItem
                      key={`audio-${track.index}`}
                      onSelect={() => onSelectAudioTrack(track.index)}
                    >
                      <span className="flex-1">{trackLabel(track)}</span>
                      {active ? <Check size={14} className="text-primary" /> : null}
                    </DropdownMenuItem>
                  );
                })
              )}
            </DropdownMenuGroup>

            <DropdownMenuSeparator />

            <div className="flex items-center justify-between px-2.5 py-2">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Subtitles
              </span>
              {!subtitlesReady && subtitleTracks.length > 0 ? (
                <span className="text-[11px] font-medium text-muted-foreground">not loaded</span>
              ) : null}
            </div>

            <DropdownMenuGroup>
              <DropdownMenuItem onSelect={() => onSelectSubtitleTrack(null)}>
                <span className="flex-1">Off</span>
                {selectedSubtitleTrack === null ? <Check size={14} className="text-primary" /> : null}
              </DropdownMenuItem>
              {subtitleTracks.length === 0 ? (
                <DropdownMenuItem disabled>No subtitle tracks</DropdownMenuItem>
              ) : (
                subtitleTracks.map((track) => (
                  <DropdownMenuItem
                    key={`subtitle-${track.index}`}
                    disabled={!subtitlesReady}
                    onSelect={() => onSelectSubtitleTrack(track.index)}
                  >
                    <span className="flex-1">{trackLabel(track)}</span>
                    {selectedSubtitleTrack === track.index ? (
                      <Check size={14} className="text-primary" />
                    ) : null}
                  </DropdownMenuItem>
                ))
              )}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>

        <DropdownMenu open={speedMenuOpen} onOpenChange={setSpeedMenuOpen}>
          <DropdownMenuTrigger asChild>
            <button
              className={ctrlBtnClassName}
              title="Playback speed"
              aria-label="Playback speed"
            >
              <span className="text-xs font-semibold tabular-nums">
                {playbackRate === 1 ? '1x' : `${playbackRate}x`}
              </span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            portalContainer={dropdownPortalContainer}
            className="w-[min(70vw,180px)] min-w-[160px] max-h-[min(70dvh,560px)] overflow-y-auto overscroll-contain"
          >
            <DropdownMenuLabel>Speed</DropdownMenuLabel>
            {[0.25, 0.5, 0.75, 1, 1.25, 1.5, 2].map((rate) => (
              <DropdownMenuItem
                key={rate}
                onSelect={() => {
                  const video = videoRef.current;
                  if (video) video.playbackRate = rate;
                }}
              >
                <span className="flex-1">{rate === 1 ? 'Normal' : `${rate}x`}</span>
                {playbackRate === rate ? <Check size={14} className="text-primary" /> : null}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        {handleOpenInfo && torrentId ? (
          <button
            className={ctrlBtnClassName}
            onClick={handleOpenInfo}
            title="Torrent info"
            aria-label="Torrent info"
          >
            <Info size={18} />
          </button>
        ) : null}
        <button className={ctrlBtnClassName} onClick={takeScreenshot} title="Screenshot (S)">
          <Camera size={18} />
        </button>
        <button
          className={ctrlBtnClassName}
          onClick={toggleFullscreen}
          title={isFullscreen ? 'Exit fullscreen (F)' : 'Fullscreen (F)'}
          aria-label={isFullscreen ? 'Exit fullscreen' : 'Fullscreen'}
          disabled={!streamUrl}
        >
          {isFullscreen ? <Minimize2 size={18} /> : <Maximize2 size={18} />}
        </button>
      </div>
    </div>
  );
};
