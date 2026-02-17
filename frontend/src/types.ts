export type TorrentStatusFilter = 'all' | 'active' | 'completed' | 'stopped';
export type TorrentView = 'summary' | 'full';
export type TorrentSortBy = 'name' | 'createdAt' | 'updatedAt' | 'totalBytes' | 'progress';
export type SortOrder = 'asc' | 'desc';

export interface FileRef {
  index: number;
  path: string;
  length: number;
  bytesCompleted?: number;
}

export interface TorrentRecord {
  id: string;
  name?: string;
  status: string;
  infoHash?: string;
  files?: FileRef[];
  totalBytes?: number;
  doneBytes?: number;
  createdAt?: string;
  updatedAt?: string;
  tags?: string[];
}

export interface TorrentSummary {
  id: string;
  name?: string;
  status: string;
  progress?: number;
  doneBytes?: number;
  totalBytes?: number;
  createdAt?: string;
  updatedAt?: string;
  tags?: string[];
}

export interface TorrentListFull {
  items: TorrentRecord[];
  count: number;
}

export interface TorrentListSummary {
  items: TorrentSummary[];
  count: number;
}

export interface SessionState {
  id: string;
  status?: string;
  progress?: number;
  peers?: number;
  downloadSpeed?: number;
  uploadSpeed?: number;
  files?: FileRef[];
  numPieces?: number;
  pieceBitfield?: string;
  updatedAt?: string;
}

export interface SessionStateList {
  items: SessionState[];
  count: number;
}

export interface MediaTrack {
  index: number;
  type: 'audio' | 'subtitle';
  codec: string;
  language: string;
  title: string;
  default: boolean;
}

export interface MediaInfo {
  tracks: MediaTrack[];
  duration: number;
  startTime?: number;
  subtitlesReady: boolean;
}

export interface ApiErrorPayload {
  error?: {
    code?: string;
    message?: string;
  };
}

export interface StorageSettings {
  mode: string;
  memoryLimitBytes: number;
  spillToDisk: boolean;
  dataDir?: string;
  hlsDir?: string;
}

export interface UpdateStorageSettingsInput {
  memoryLimitBytes: number;
}

export interface EncodingSettings {
  preset: string;
  crf: number;
  audioBitrate: string;
}

export interface HLSSettings {
  memBufSizeMB: number;
  cacheSizeMB: number;
  cacheMaxAgeHours: number;
  segmentDuration: number;
}

export interface PlayerSettings {
  currentTorrentId?: string;
}

export interface WatchPosition {
  torrentId: string;
  fileIndex: number;
  position: number;
  duration: number;
  torrentName: string;
  filePath: string;
  updatedAt: string;
}

export interface HlsHealthSnapshot {
  activeJobs: number;
  totalJobStarts: number;
  totalJobFailures: number;
  totalSeekRequests: number;
  totalSeekFailures: number;
  totalAutoRestarts: number;
  lastJobStartedAt?: string;
  lastPlaylistReady?: string;
  lastJobError?: string;
  lastJobErrorAt?: string;
  lastSeekAt?: string;
  lastSeekTarget?: number;
  lastSeekError?: string;
  lastSeekErrorAt?: string;
  lastAutoRestartAt?: string;
  lastAutoRestartReason?: string;
}

export interface PlayerHealth {
  status: 'ok' | 'degraded';
  checkedAt: string;
  currentTorrentId?: string;
  focusModeEnabled: boolean;
  activeSessions: number;
  activeSessionIds?: string[];
  hls: HlsHealthSnapshot;
  issues?: string[];
}

export interface BulkResultItem {
  id: string;
  ok: boolean;
  error?: string;
}

export interface BulkResponse {
  items: BulkResultItem[];
}

export type SearchSortBy = 'relevance' | 'seeders' | 'sizeBytes' | 'publishedAt';
export type SearchSortOrder = 'asc' | 'desc';

export interface SearchProviderInfo {
  name: string;
  label: string;
  kind: string;
  enabled: boolean;
}

export interface SearchProviderRuntimeConfig {
  name: string;
  label: string;
  endpoint?: string;
  proxyUrl?: string;
  hasApiKey: boolean;
  apiKeyPreview?: string;
  configured: boolean;
}

export interface SearchProviderRuntimePatch {
  provider: string;
  endpoint?: string;
  apiKey?: string;
  proxyUrl?: string;
}

export type SearchProviderAutodetectStatus = 'detected' | 'already_configured' | 'not_found' | 'error';

export interface SearchProviderAutodetectResult {
  provider: string;
  ok: boolean;
  status: SearchProviderAutodetectStatus;
  method?: string;
  message: string;
}

export interface FlareSolverrProviderStatus {
  provider: string;
  configured: boolean;
  url?: string;
  message?: string;
}

export interface FlareSolverrSettings {
  defaultUrl: string;
  url?: string;
  providers: FlareSolverrProviderStatus[];
}

export interface FlareSolverrApplyResult {
  provider: string;
  ok: boolean;
  status: string;
  message: string;
}

export interface FlareSolverrApplyResponse {
  url: string;
  results: FlareSolverrApplyResult[];
}

export interface SubIndexerInfo {
  id: string;
  name: string;
}

export interface SearchProviderDiagnostics {
  name: string;
  label: string;
  kind: string;
  enabled: boolean;
  consecutiveFailures: number;
  blockedUntil?: string;
  lastError?: string;
  lastSuccessAt?: string;
  lastFailureAt?: string;
  lastLatencyMs?: number;
  lastTimeout?: boolean;
  lastQuery?: string;
  totalRequests?: number;
  totalFailures?: number;
  timeoutCount?: number;
  fanOut?: boolean;
  subIndexers?: SubIndexerInfo[];
}

export interface SearchProviderTestResult {
  provider: string;
  query: string;
  ok: boolean;
  count?: number;
  elapsedMs: number;
  error?: string;
  sample?: string[];
}

export interface SearchProviderStatus {
  name: string;
  ok: boolean;
  count: number;
  error?: string;
}

export interface SearchSourceRef {
  name: string;
  tracker?: string;
}

export interface SearchResultItem {
  name: string;
  infoHash?: string;
  magnet?: string;
  pageUrl?: string;
  sizeBytes?: number;
  seeders?: number;
  leechers?: number;
  source?: string;
  tracker?: string;
  sources?: SearchSourceRef[];
  publishedAt?: string;
  enrichment?: SearchEnrichment;
}

export interface SearchDubbingInfo {
  type?: string;
  group?: string;
  groups?: string[];
}

export interface SearchEnrichment {
  description?: string;
  nfo?: string;
  poster?: string;
  screenshots?: string[];
  quality?: string;
  audio?: string[];
  subtitles?: string[];
  isSeries?: boolean;
  season?: number;
  episode?: number;
  year?: number;
  dubbing?: SearchDubbingInfo;
  sourceType?: string;
  hdr?: boolean;
  dolbyVision?: boolean;
  audioChannels?: string;
  contentType?: string;
  tmdbId?: number;
  tmdbPoster?: string;
  tmdbRating?: number;
  tmdbOverview?: string;
}

export interface SearchRankingProfile {
  freshnessWeight: number;
  seedersWeight: number;
  qualityWeight: number;
  languageWeight: number;
  sizeWeight: number;
  preferSeries: boolean;
  preferMovies: boolean;
  preferredAudio: string[];
  preferredSubtitles: string[];
  targetSizeBytes: number;
}

export interface SearchResponse {
  query: string;
  items: SearchResultItem[];
  providers: SearchProviderStatus[];
  elapsedMs: number;
  totalItems: number;
  limit: number;
  offset: number;
  hasMore: boolean;
  sortBy: SearchSortBy;
  sortOrder: SearchSortOrder;
  phase?: 'bootstrap' | 'fast' | 'full' | 'update';
  provider?: string;
  final?: boolean;
}
