package search

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"torrentstream/searchservice/internal/domain"
)

var (
	tokenPattern          = regexp.MustCompile(`[\p{L}\p{N}]+`)
	yearPattern           = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	seasonEpisodePattern  = regexp.MustCompile(`(?i)s\s*(\d{1,2})\s*e\s*(\d{1,3})`)
	seasonXEpisodePattern = regexp.MustCompile(`(?i)(\d{1,2})x(\d{1,3})`)
	seasonPattern         = regexp.MustCompile(`(?i)(?:season|\x{0441}\x{0435}\x{0437}\x{043e}\x{043d}|\x{0441}\x{0435}\x{0437})\s*(\d{1,2})`)
	episodePattern        = regexp.MustCompile(`(?i)(?:episode|ep|\x{0441}\x{0435}\x{0440}\x{0438}\x{044f}|\x{0441}\x{0435}\x{0440})\s*(\d{1,3})`)
)

var stopwordTokens = map[string]struct{}{
	"1080p": {}, "2160p": {}, "720p": {}, "480p": {},
	"x264": {}, "h264": {}, "x265": {}, "hevc": {}, "av1": {},
	"hdr": {}, "hdr10": {}, "webrip": {}, "web": {}, "webdl": {}, "web-dl": {},
	"bluray": {}, "bdrip": {}, "dvdrip": {}, "hdrip": {}, "camrip": {}, "remux": {},
	"aac": {}, "ac3": {}, "dts": {}, "mp3": {}, "flac": {},
	"rus": {}, "eng": {}, "russian": {}, "english": {}, "sub": {}, "subs": {},
	"mkv": {}, "mp4": {}, "avi": {}, "torrent": {}, "proper": {}, "repack": {},
	"dl": {}, "season": {}, "episode": {}, "ep": {},
	"\u0441\u0435\u0437\u043e\u043d": {}, "\u0441\u0435\u0437": {}, "\u0441\u0435\u0440\u0438\u044f": {}, "\u0441\u0435\u0440": {},
}

type titleMeta struct {
	normalized string
	tokens     []string
	tokenSet   map[string]struct{}
	year       int
	season     int
	episode    int
}

func parseTitleMeta(raw string) titleMeta {
	input := strings.ToLower(strings.TrimSpace(raw))
	input = strings.ReplaceAll(input, "\u0451", "\u0435")
	if input == "" {
		return titleMeta{tokenSet: map[string]struct{}{}}
	}

	meta := titleMeta{
		tokenSet: make(map[string]struct{}),
	}

	meta.year = extractYear(input)
	meta.season, meta.episode = extractSeasonEpisode(input)

	matches := tokenPattern.FindAllString(input, -1)
	for _, match := range matches {
		token := strings.TrimSpace(match)
		if token == "" {
			continue
		}
		if _, ok := stopwordTokens[token]; ok {
			continue
		}
		if seasonEpisodePattern.MatchString(token) || seasonXEpisodePattern.MatchString(token) {
			continue
		}
		if isResolutionToken(token) {
			continue
		}
		if numeric, err := strconv.Atoi(token); err == nil {
			if (meta.year > 0 && numeric == meta.year) || (meta.season > 0 && numeric == meta.season) || (meta.episode > 0 && numeric == meta.episode) {
				continue
			}
		}
		if _, exists := meta.tokenSet[token]; !exists {
			meta.tokens = append(meta.tokens, token)
			meta.tokenSet[token] = struct{}{}
		}

		translit := transliterateCyrillic(token)
		if translit != "" && translit != token {
			if _, exists := meta.tokenSet[translit]; !exists {
				meta.tokenSet[translit] = struct{}{}
			}
		}
	}

	meta.normalized = strings.Join(meta.tokens, " ")
	return meta
}

func extractYear(input string) int {
	matches := yearPattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return 0
	}
	year := 0
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if value > year {
			year = value
		}
	}
	return year
}

func extractSeasonEpisode(input string) (int, int) {
	if match := seasonEpisodePattern.FindStringSubmatch(input); len(match) >= 3 {
		season := parseIntOrZero(match[1])
		episode := parseIntOrZero(match[2])
		return season, episode
	}
	if match := seasonXEpisodePattern.FindStringSubmatch(input); len(match) >= 3 {
		season := parseIntOrZero(match[1])
		episode := parseIntOrZero(match[2])
		return season, episode
	}

	season := 0
	episode := 0
	if match := seasonPattern.FindStringSubmatch(input); len(match) >= 2 {
		season = parseIntOrZero(match[1])
	}
	if match := episodePattern.FindStringSubmatch(input); len(match) >= 2 {
		episode = parseIntOrZero(match[1])
	}
	return season, episode
}

func parseIntOrZero(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func isResolutionToken(token string) bool {
	if len(token) < 3 || len(token) > 5 {
		return false
	}
	if !strings.HasSuffix(token, "p") {
		return false
	}
	_, err := strconv.Atoi(strings.TrimSuffix(token, "p"))
	return err == nil
}

func transliterateCyrillic(input string) string {
	var builder strings.Builder
	builder.Grow(len(input))
	for _, r := range input {
		if mapped, ok := cyrillicToLatin[r]; ok {
			builder.WriteString(mapped)
			continue
		}
		if unicode.Is(unicode.Cyrillic, r) {
			continue
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}

var cyrillicToLatin = map[rune]string{
	'\u0430': "a", '\u0431': "b", '\u0432': "v", '\u0433': "g", '\u0434': "d", '\u0435': "e", '\u0436': "zh", '\u0437': "z",
	'\u0438': "i", '\u0439': "i", '\u043a': "k", '\u043b': "l", '\u043c': "m", '\u043d': "n", '\u043e': "o", '\u043f': "p",
	'\u0440': "r", '\u0441': "s", '\u0442': "t", '\u0443': "u", '\u0444': "f", '\u0445': "h", '\u0446': "ts", '\u0447': "ch",
	'\u0448': "sh", '\u0449': "sch", '\u044b': "y", '\u044d': "e", '\u044e': "yu", '\u044f': "ya", '\u044c': "", '\u044a': "",
}

func normalizeInfoHash(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	value = strings.TrimPrefix(value, "urn:btih:")
	return value
}

func extractInfoHashFromMagnet(rawMagnet string) string {
	value := strings.TrimSpace(rawMagnet)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	for _, xt := range parsed.Query()["xt"] {
		hash := normalizeInfoHash(xt)
		if hash != "" {
			return hash
		}
	}
	return ""
}

func buildTitleDedupeKey(item domain.SearchResult) string {
	meta := parseTitleMeta(item.Name)
	if meta.normalized == "" {
		return ""
	}

	parts := []string{"title", meta.normalized}
	if meta.year > 0 {
		parts = append(parts, fmt.Sprintf("y:%d", meta.year))
	}
	if meta.season > 0 {
		parts = append(parts, fmt.Sprintf("s:%d", meta.season))
	}
	if meta.episode > 0 {
		parts = append(parts, fmt.Sprintf("e:%d", meta.episode))
	}
	if item.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("b:%d", item.SizeBytes/(64*1024*1024)))
	}
	return strings.Join(parts, "|")
}

func enrichSearchResult(item domain.SearchResult) domain.SearchResult {
	parsed := parseEnrichmentFromTitle(item.Name)
	item.Enrichment = mergeEnrichment(item.Enrichment, parsed)

	// Detect dubbing from title (and description if available)
	dubbingSource := item.Name
	if item.Enrichment.Description != "" {
		dubbingSource = dubbingSource + " " + item.Enrichment.Description
	}
	dubbing := detectDubbing(dubbingSource)
	if dubbing.Type != domain.DubbingUnknown || len(dubbing.Groups) > 0 {
		item.Enrichment.Dubbing = dubbing
	}

	if item.Enrichment.Description == "" {
		item.Enrichment.Description = buildEnrichmentDescription(item)
	}
	if item.Enrichment.NFO == "" {
		item.Enrichment.NFO = buildEnrichmentNFO(item)
	}
	return item
}

func parseEnrichmentFromTitle(name string) domain.SearchEnrichment {
	meta := parseTitleMeta(name)
	rawLower := strings.ToLower(strings.TrimSpace(name))

	quality := detectQuality(rawLower)
	audio := detectAudioHints(rawLower)
	subtitles := detectSubtitleHints(rawLower)
	sourceType := detectSourceType(rawLower)
	hdr, dv := detectHDR(rawLower)
	channels := detectAudioChannels(rawLower)
	contentType := detectContentType(rawLower, meta)

	enrichment := domain.SearchEnrichment{
		Quality:       quality,
		Audio:         audio,
		Subtitles:     subtitles,
		Year:          meta.year,
		Season:        meta.season,
		Episode:       meta.episode,
		SourceType:    sourceType,
		HDR:           hdr,
		DolbyVision:   dv,
		AudioChannels: channels,
		ContentType:   contentType,
	}
	enrichment.IsSeries = meta.season > 0 || meta.episode > 0 || strings.Contains(rawLower, "series") || strings.Contains(rawLower, "сезон")
	return enrichment
}

func detectQuality(rawLower string) string {
	parts := make([]string, 0, 3)

	resolution := ""
	switch {
	case strings.Contains(rawLower, "2160p") || strings.Contains(rawLower, "4k"):
		resolution = "2160p"
	case strings.Contains(rawLower, "1440p"):
		resolution = "1440p"
	case strings.Contains(rawLower, "1080p"):
		resolution = "1080p"
	case strings.Contains(rawLower, "720p"):
		resolution = "720p"
	case strings.Contains(rawLower, "480p"):
		resolution = "480p"
	}
	if resolution != "" {
		parts = append(parts, resolution)
	}

	source := ""
	switch {
	case strings.Contains(rawLower, "bluray"):
		source = "BluRay"
	case strings.Contains(rawLower, "bdrip"):
		source = "BDRip"
	case strings.Contains(rawLower, "webrip"):
		source = "WEBRip"
	case strings.Contains(rawLower, "web-dl") || strings.Contains(rawLower, "webdl"):
		source = "WEB-DL"
	case strings.Contains(rawLower, "dvdrip"):
		source = "DVDRip"
	case strings.Contains(rawLower, "cam"):
		source = "CAM"
	}
	if source != "" {
		parts = append(parts, source)
	}

	codec := ""
	switch {
	case strings.Contains(rawLower, "av1"):
		codec = "AV1"
	case strings.Contains(rawLower, "x265") || strings.Contains(rawLower, "h265") || strings.Contains(rawLower, "hevc"):
		codec = "H.265"
	case strings.Contains(rawLower, "x264") || strings.Contains(rawLower, "h264"):
		codec = "H.264"
	}
	if codec != "" {
		parts = append(parts, codec)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func detectAudioHints(rawLower string) []string {
	items := make([]string, 0, 6)
	appendIfContains := func(needle, label string) {
		if strings.Contains(rawLower, needle) {
			items = append(items, label)
		}
	}

	appendIfContains("atmos", "Atmos")
	appendIfContains("truehd", "TrueHD")
	appendIfContains("dts", "DTS")
	appendIfContains("eac3", "E-AC3")
	appendIfContains("ac3", "AC3")
	appendIfContains("aac", "AAC")
	appendIfContains("flac", "FLAC")
	appendIfContains("mp3", "MP3")

	items = append(items, detectLanguageHints(rawLower)...)
	return uniqueStrings(items)
}

func detectSubtitleHints(rawLower string) []string {
	hasSubs := strings.Contains(rawLower, "sub") || strings.Contains(rawLower, "subs") || strings.Contains(rawLower, "subtitle") || strings.Contains(rawLower, "суб")
	if !hasSubs {
		return nil
	}
	langs := detectLanguageHints(rawLower)
	if len(langs) == 0 {
		return []string{"Unknown"}
	}
	return langs
}

func detectLanguageHints(rawLower string) []string {
	items := make([]string, 0, 4)
	for _, token := range tokenPattern.FindAllString(rawLower, -1) {
		switch token {
		case "ru", "rus", "russian", "рус", "русский", "русская", "русское":
			items = append(items, "RU")
		case "en", "eng", "english", "англ", "английский":
			items = append(items, "EN")
		case "uk", "ukr", "ukrainian", "укр", "украинский":
			items = append(items, "UK")
		case "multi", "multiaudio", "multilang":
			items = append(items, "MULTI")
		}
		if strings.Contains(token, "многоголос") || strings.Contains(token, "мультияз") {
			items = append(items, "MULTI")
		}
	}
	return uniqueStrings(items)
}

func mergeEnrichment(base, parsed domain.SearchEnrichment) domain.SearchEnrichment {
	merged := base
	if merged.Description == "" {
		merged.Description = parsed.Description
	}
	if merged.NFO == "" {
		merged.NFO = parsed.NFO
	}
	if merged.Poster == "" {
		merged.Poster = parsed.Poster
	}
	if len(merged.Screenshots) == 0 {
		merged.Screenshots = append([]string(nil), parsed.Screenshots...)
	} else {
		merged.Screenshots = uniqueStrings(append(append([]string(nil), merged.Screenshots...), parsed.Screenshots...))
	}
	if merged.Quality == "" {
		merged.Quality = parsed.Quality
	}
	if len(merged.Audio) == 0 {
		merged.Audio = append([]string(nil), parsed.Audio...)
	} else {
		merged.Audio = uniqueStrings(append(append([]string(nil), merged.Audio...), parsed.Audio...))
	}
	if len(merged.Subtitles) == 0 {
		merged.Subtitles = append([]string(nil), parsed.Subtitles...)
	} else {
		merged.Subtitles = uniqueStrings(append(append([]string(nil), merged.Subtitles...), parsed.Subtitles...))
	}
	if !merged.IsSeries {
		merged.IsSeries = parsed.IsSeries
	}
	if merged.Season == 0 {
		merged.Season = parsed.Season
	}
	if merged.Episode == 0 {
		merged.Episode = parsed.Episode
	}
	if merged.Year == 0 {
		merged.Year = parsed.Year
	}
	if merged.Dubbing.Type == domain.DubbingUnknown && parsed.Dubbing.Type != domain.DubbingUnknown {
		merged.Dubbing = parsed.Dubbing
	} else if len(merged.Dubbing.Groups) == 0 && len(parsed.Dubbing.Groups) > 0 {
		merged.Dubbing = parsed.Dubbing
	}
	if merged.SourceType == "" {
		merged.SourceType = parsed.SourceType
	}
	if !merged.HDR {
		merged.HDR = parsed.HDR
	}
	if !merged.DolbyVision {
		merged.DolbyVision = parsed.DolbyVision
	}
	if merged.AudioChannels == "" {
		merged.AudioChannels = parsed.AudioChannels
	}
	if merged.ContentType == "" {
		merged.ContentType = parsed.ContentType
	}
	return merged
}

func buildEnrichmentDescription(item domain.SearchResult) string {
	parts := make([]string, 0, 6)
	if item.Enrichment.Quality != "" {
		parts = append(parts, item.Enrichment.Quality)
	}
	if len(item.Enrichment.Audio) > 0 {
		parts = append(parts, "Audio: "+strings.Join(item.Enrichment.Audio, ", "))
	}
	if len(item.Enrichment.Subtitles) > 0 {
		parts = append(parts, "Subs: "+strings.Join(item.Enrichment.Subtitles, ", "))
	}
	if item.Enrichment.Year > 0 {
		parts = append(parts, fmt.Sprintf("Year: %d", item.Enrichment.Year))
	}
	if item.SizeBytes > 0 {
		parts = append(parts, "Size: "+humanBytes(item.SizeBytes))
	}
	if item.Source != "" {
		parts = append(parts, "Source: "+item.Source)
	}
	return strings.Join(parts, " \u00b7 ")
}

func buildEnrichmentNFO(item domain.SearchResult) string {
	fields := make([]string, 0, 4)
	if item.Enrichment.Quality != "" {
		fields = append(fields, "quality="+item.Enrichment.Quality)
	}
	if len(item.Enrichment.Audio) > 0 {
		fields = append(fields, "audio="+strings.Join(item.Enrichment.Audio, ","))
	}
	if len(item.Enrichment.Subtitles) > 0 {
		fields = append(fields, "subs="+strings.Join(item.Enrichment.Subtitles, ","))
	}
	if item.Enrichment.IsSeries {
		if item.Enrichment.Season > 0 || item.Enrichment.Episode > 0 {
			fields = append(fields, fmt.Sprintf("series=s%02de%02d", item.Enrichment.Season, item.Enrichment.Episode))
		} else {
			fields = append(fields, "series=true")
		}
	}
	return strings.Join(fields, "; ")
}

func humanBytes(size int64) string {
	if size <= 0 {
		return "0 B"
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	value := float64(size) / float64(div)
	return fmt.Sprintf("%.1f %ciB", value, "KMGTPE"[exp])
}

func uniqueStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func relevanceScoreForResult(queryMeta titleMeta, profile domain.SearchRankingProfile, item domain.SearchResult) float64 {
	itemMeta := parseTitleMeta(item.Name)
	return relevanceScoreFromMeta(queryMeta, itemMeta, profile, item)
}

func relevanceScoreFromMeta(queryMeta, itemMeta titleMeta, profile domain.SearchRankingProfile, item domain.SearchResult) float64 {
	score := 0.0
	queryTokenCount := len(queryMeta.tokenSet)

	if queryTokenCount > 0 {
		matches := 0
		for token := range queryMeta.tokenSet {
			if _, ok := itemMeta.tokenSet[token]; ok {
				matches++
			}
		}
		coverage := float64(matches) / float64(queryTokenCount)
		score += coverage * 100
		if matches == queryTokenCount {
			score += 12
		}
	}

	if queryMeta.normalized != "" && strings.Contains(itemMeta.normalized, queryMeta.normalized) {
		score += 30
	}

	if queryMeta.year > 0 {
		switch {
		case itemMeta.year == queryMeta.year:
			score += 22
		case itemMeta.year > 0:
			score -= 14
		}
	}

	if queryMeta.season > 0 {
		switch {
		case itemMeta.season == queryMeta.season:
			score += 18
		case itemMeta.season > 0:
			score -= 20
		}
	}

	if queryMeta.episode > 0 {
		switch {
		case itemMeta.episode == queryMeta.episode:
			score += 14
		case itemMeta.episode > 0:
			score -= 16
		}
	}

	enrichment := item.Enrichment
	qualityComponent := qualityComponentScore(enrichment)
	seedComponent := math.Log1p(math.Max(float64(item.Seeders), 0))*3 + math.Log1p(math.Max(float64(item.Leechers), 0))*1.5
	score += profile.SeedersWeight * seedComponent
	score += profile.QualityWeight * qualityComponent
	score += profile.LanguageWeight * languagePreferenceScore(profile, enrichment)
	score += profile.SizeWeight * sizePreferenceScore(profile, enrichment, item.SizeBytes)

	if item.PublishedAt != nil {
		ageDays := time.Since(*item.PublishedAt).Hours() / 24
		if ageDays > 0 {
			fresh := math.Max(0, 12-math.Min(ageDays/30, 12))
			score += profile.FreshnessWeight * fresh
		}
	}

	if profile.PreferSeries && enrichment.IsSeries {
		score += 6
	}
	if profile.PreferMovies && !enrichment.IsSeries {
		score += 6
	}
	if profile.PreferSeries && !profile.PreferMovies && !enrichment.IsSeries {
		score -= 3
	}
	if profile.PreferMovies && !profile.PreferSeries && enrichment.IsSeries {
		score -= 3
	}

	if strings.TrimSpace(item.Magnet) != "" || strings.TrimSpace(item.InfoHash) != "" {
		score += 4
	}

	tokenDelta := math.Abs(float64(len(itemMeta.tokens) - len(queryMeta.tokens)))
	score -= tokenDelta * 0.7

	return score
}

func detectSourceType(rawLower string) string {
	switch {
	case strings.Contains(rawLower, "remux"):
		return "Remux"
	case strings.Contains(rawLower, "bluray") || strings.Contains(rawLower, "blu-ray"):
		return "BluRay"
	case strings.Contains(rawLower, "web-dl") || strings.Contains(rawLower, "webdl"):
		return "WEB-DL"
	case strings.Contains(rawLower, "bdrip"):
		return "BDRip"
	case strings.Contains(rawLower, "webrip"):
		return "WEBRip"
	case strings.Contains(rawLower, "hdrip"):
		return "HDRip"
	case strings.Contains(rawLower, "dvdrip"):
		return "DVDRip"
	case strings.Contains(rawLower, "hdtv"):
		return "HDTV"
	case strings.Contains(rawLower, "telesync") || strings.Contains(rawLower, " ts "):
		return "TS"
	case strings.Contains(rawLower, "camrip") || strings.Contains(rawLower, " cam "):
		return "CAM"
	default:
		return ""
	}
}

func detectHDR(rawLower string) (hdr bool, dv bool) {
	hdr = strings.Contains(rawLower, "hdr10+") || strings.Contains(rawLower, "hdr10") || strings.Contains(rawLower, " hdr ")
	dv = strings.Contains(rawLower, "dolby vision") || strings.Contains(rawLower, "dolbyvision") || strings.Contains(rawLower, " dv ")
	return
}

func detectAudioChannels(rawLower string) string {
	switch {
	case strings.Contains(rawLower, "7.1"):
		return "7.1"
	case strings.Contains(rawLower, "5.1"):
		return "5.1"
	case strings.Contains(rawLower, "2.0") || strings.Contains(rawLower, "stereo"):
		return "2.0"
	default:
		return ""
	}
}

func detectContentType(rawLower string, meta titleMeta) string {
	if meta.season > 0 || meta.episode > 0 || strings.Contains(rawLower, "сезон") || strings.Contains(rawLower, "season") || strings.Contains(rawLower, "series") {
		if strings.Contains(rawLower, "anime") || strings.Contains(rawLower, "аниме") {
			return "anime"
		}
		return "series"
	}
	if strings.Contains(rawLower, "anime") || strings.Contains(rawLower, "аниме") {
		return "anime"
	}
	return "movie"
}

func sourceTypeScore(sourceType string) float64 {
	switch sourceType {
	case "Remux":
		return 10
	case "BluRay":
		return 9
	case "WEB-DL":
		return 8
	case "BDRip":
		return 7
	case "WEBRip":
		return 6
	case "HDRip":
		return 4
	case "DVDRip":
		return 3
	case "HDTV":
		return 3
	case "TS":
		return 1
	case "CAM":
		return 0
	default:
		return 2
	}
}

func qualityComponentScore(enrichment domain.SearchEnrichment) float64 {
	quality := strings.ToLower(strings.TrimSpace(enrichment.Quality))
	score := 0.0

	// Resolution
	switch {
	case strings.Contains(quality, "2160") || strings.Contains(quality, "4k"):
		score += 8
	case strings.Contains(quality, "1440"):
		score += 7
	case strings.Contains(quality, "1080"):
		score += 6
	case strings.Contains(quality, "720"):
		score += 4
	case strings.Contains(quality, "480"):
		score += 2
	}

	// Codec
	if strings.Contains(quality, "hevc") || strings.Contains(quality, "h.265") || strings.Contains(quality, "av1") {
		score += 1
	}
	if strings.Contains(quality, "cam") {
		score -= 3
	}

	// Source type hierarchy
	score += sourceTypeScore(enrichment.SourceType) * 0.7

	// HDR / Dolby Vision
	if enrichment.HDR {
		score += 2
	}
	if enrichment.DolbyVision {
		score += 1
	}

	// Audio channels
	switch enrichment.AudioChannels {
	case "7.1":
		score += 1.5
	case "5.1":
		score += 1
	}

	// Dubbing quality bonus
	score += dubbingScore(enrichment.Dubbing)

	return score
}

func languagePreferenceScore(profile domain.SearchRankingProfile, enrichment domain.SearchEnrichment) float64 {
	preferredAudio := normalizeLangPreferences(profile.PreferredAudio)
	preferredSubs := normalizeLangPreferences(profile.PreferredSubtitles)

	audioLangs := extractLanguageHints(enrichment.Audio)
	subLangs := extractLanguageHints(enrichment.Subtitles)

	score := 0.0
	if len(preferredAudio) == 0 && len(preferredSubs) == 0 {
		if len(audioLangs) > 0 || len(subLangs) > 0 {
			return 1
		}
		return 0
	}

	for _, pref := range preferredAudio {
		if _, ok := audioLangs[pref]; ok {
			score += 4
		}
	}
	for _, pref := range preferredSubs {
		if _, ok := subLangs[pref]; ok {
			score += 3
		}
	}
	if score > 0 {
		return score
	}

	// If user has a preference and we have no matching hints, de-prioritize unknown results
	// so explicit RU/EN tagged items rise to the top.
	if len(audioLangs) > 0 || len(subLangs) > 0 {
		return -4
	}
	return -3
}

func sizePreferenceScore(profile domain.SearchRankingProfile, enrichment domain.SearchEnrichment, sizeBytes int64) float64 {
	if sizeBytes <= 0 {
		return 0
	}
	target := profile.TargetSizeBytes
	if target <= 0 {
		if enrichment.IsSeries {
			target = 2 * 1024 * 1024 * 1024
		} else {
			target = 6 * 1024 * 1024 * 1024
		}
	}
	if target <= 0 {
		return 0
	}
	delta := math.Abs(float64(sizeBytes-target)) / float64(target)
	return math.Max(0, 5-delta*5)
}

func compareFloat64(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func normalizeLangPreferences(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		token := normalizeLanguageToken(value)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func extractLanguageHints(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, 4)
	for _, value := range values {
		if token := normalizeLanguageToken(value); token != "" {
			out[token] = struct{}{}
		}
	}
	return out
}

func normalizeLanguageToken(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	switch raw {
	case "ru", "rus", "russian", "рус":
		return "ru"
	case "en", "eng", "english", "англ":
		return "en"
	case "uk", "ukr", "ukrainian", "укр":
		return "uk"
	case "multi", "multilang", "multiaudio", "мульти":
		return "multi"
	default:
		return ""
	}
}
