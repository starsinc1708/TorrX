package apihttp

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"torrentstream/internal/domain"
)

type torrentRecordView struct {
	domain.TorrentRecord
	MediaOrganization *mediaOrganization `json:"mediaOrganization,omitempty"`
}

type mediaOrganization struct {
	ContentType string                   `json:"contentType"`
	Groups      []mediaOrganizationGroup `json:"groups,omitempty"`
}

type mediaOrganizationGroup struct {
	ID     string                  `json:"id"`
	Type   string                  `json:"type"`
	Title  string                  `json:"title"`
	Season int                     `json:"season,omitempty"`
	Items  []mediaOrganizationItem `json:"items"`
}

type mediaOrganizationItem struct {
	FileIndex   int    `json:"fileIndex"`
	FilePath    string `json:"filePath"`
	DisplayName string `json:"displayName"`
	Season      int    `json:"season,omitempty"`
	Episode     int    `json:"episode,omitempty"`
	Year        int    `json:"year,omitempty"`
}

type mediaParsedFile struct {
	file        domain.FileRef
	displayName string
	kind        string // series | movie | other
	season      int
	episode     int
	year        int
}

var (
	seriesPatternSxE = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3})\b`)
	seriesPatternX   = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	episodePattern   = regexp.MustCompile(`(?i)\bE(?:P)?[ ._-]?(\d{1,3})\b`)
	seasonPattern    = regexp.MustCompile(`(?i)\b(?:season|s)[ ._-]?(\d{1,2})\b`)
	yearPattern      = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	noiseToken       = regexp.MustCompile(`(?i)\b(480p|720p|1080p|2160p|x264|x265|h264|h265|hevc|bluray|bdrip|web[- ]?dl|webrip|dvdrip|hdrip|aac|ac3|dts|proper|repack|remux|extended|10bit|8bit)\b`)
	separatorPattern = regexp.MustCompile(`[._]+`)
	spacePattern     = regexp.MustCompile(`\s+`)
)

var mediaVideoExtensions = map[string]struct{}{
	".mp4": {}, ".m4v": {}, ".mov": {}, ".mkv": {}, ".avi": {},
	".wmv": {}, ".flv": {}, ".webm": {}, ".ts": {}, ".m2ts": {},
}

func buildTorrentRecordView(record domain.TorrentRecord) torrentRecordView {
	return torrentRecordView{
		TorrentRecord:     record,
		MediaOrganization: buildMediaOrganization(record.Files),
	}
}

func buildMediaOrganization(files []domain.FileRef) *mediaOrganization {
	if len(files) == 0 {
		return nil
	}

	parsed := make([]mediaParsedFile, 0, len(files))
	seriesCount := 0
	movieCount := 0
	for _, file := range files {
		item := parseMediaFile(file)
		if item.kind == "series" {
			seriesCount++
		}
		if item.kind == "movie" {
			movieCount++
		}
		parsed = append(parsed, item)
	}

	contentType := "unknown"
	switch {
	case seriesCount > 0 && movieCount > 0:
		contentType = "mixed"
	case seriesCount > 0:
		contentType = "series"
	case movieCount > 0:
		contentType = "movie"
	}

	seriesBySeason := map[int][]mediaParsedFile{}
	movies := make([]mediaParsedFile, 0)
	others := make([]mediaParsedFile, 0)

	for _, item := range parsed {
		switch item.kind {
		case "series":
			seriesBySeason[item.season] = append(seriesBySeason[item.season], item)
		case "movie":
			movies = append(movies, item)
		default:
			others = append(others, item)
		}
	}

	groups := make([]mediaOrganizationGroup, 0)

	if len(seriesBySeason) > 0 {
		seasons := make([]int, 0, len(seriesBySeason))
		for season := range seriesBySeason {
			seasons = append(seasons, season)
		}
		sort.Ints(seasons)

		for _, season := range seasons {
			seasonItems := seriesBySeason[season]
			sort.SliceStable(seasonItems, func(i, j int) bool {
				if seasonItems[i].episode != seasonItems[j].episode {
					return seasonItems[i].episode < seasonItems[j].episode
				}
				return seasonItems[i].file.Index < seasonItems[j].file.Index
			})
			title := "Season " + strconv.Itoa(season)
			if season <= 0 {
				title = "Season Unknown"
			}
			groups = append(groups, mediaOrganizationGroup{
				ID:     "season-" + strconv.Itoa(season),
				Type:   "series",
				Title:  title,
				Season: season,
				Items:  buildOrganizationItems(seasonItems),
			})
		}
	}

	if len(movies) > 0 {
		sort.SliceStable(movies, func(i, j int) bool {
			if movies[i].displayName != movies[j].displayName {
				return movies[i].displayName < movies[j].displayName
			}
			return movies[i].file.Index < movies[j].file.Index
		})
		groups = append(groups, mediaOrganizationGroup{
			ID:    "movies",
			Type:  "movie",
			Title: "Movies",
			Items: buildOrganizationItems(movies),
		})
	}

	if len(others) > 0 {
		sort.SliceStable(others, func(i, j int) bool {
			return others[i].file.Index < others[j].file.Index
		})
		groups = append(groups, mediaOrganizationGroup{
			ID:    "other",
			Type:  "other",
			Title: "Other files",
			Items: buildOrganizationItems(others),
		})
	}

	return &mediaOrganization{
		ContentType: contentType,
		Groups:      groups,
	}
}

func buildOrganizationItems(parsed []mediaParsedFile) []mediaOrganizationItem {
	items := make([]mediaOrganizationItem, 0, len(parsed))
	for _, item := range parsed {
		items = append(items, mediaOrganizationItem{
			FileIndex:   item.file.Index,
			FilePath:    item.file.Path,
			DisplayName: item.displayName,
			Season:      item.season,
			Episode:     item.episode,
			Year:        item.year,
		})
	}
	return items
}

func parseMediaFile(file domain.FileRef) mediaParsedFile {
	baseName := filepath.Base(file.Path)
	ext := strings.ToLower(filepath.Ext(baseName))
	nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	displayName := normalizeMediaName(nameWithoutExt)
	if displayName == "" {
		displayName = nameWithoutExt
	}

	parsed := mediaParsedFile{
		file:        file,
		displayName: displayName,
		kind:        "other",
	}

	if yearMatch := yearPattern.FindStringSubmatch(nameWithoutExt); len(yearMatch) == 2 {
		if year, err := strconv.Atoi(yearMatch[1]); err == nil {
			parsed.year = year
		}
	}

	if _, ok := mediaVideoExtensions[ext]; !ok {
		return parsed
	}

	if m := seriesPatternSxE.FindStringSubmatch(nameWithoutExt); len(m) == 3 {
		parsed.kind = "series"
		parsed.season, _ = strconv.Atoi(m[1])
		parsed.episode, _ = strconv.Atoi(m[2])
		return parsed
	}
	if m := seriesPatternX.FindStringSubmatch(nameWithoutExt); len(m) == 3 {
		parsed.kind = "series"
		parsed.season, _ = strconv.Atoi(m[1])
		parsed.episode, _ = strconv.Atoi(m[2])
		return parsed
	}
	if m := episodePattern.FindStringSubmatch(nameWithoutExt); len(m) == 2 {
		parsed.kind = "series"
		parsed.episode, _ = strconv.Atoi(m[1])
		if season := detectSeasonFromPath(file.Path); season > 0 {
			parsed.season = season
		}
		return parsed
	}

	parsed.kind = "movie"
	return parsed
}

func detectSeasonFromPath(path string) int {
	normalized := separatorPattern.ReplaceAllString(path, " ")
	if m := seasonPattern.FindStringSubmatch(normalized); len(m) == 2 {
		if season, err := strconv.Atoi(m[1]); err == nil {
			return season
		}
	}
	return 0
}

func normalizeMediaName(name string) string {
	out := separatorPattern.ReplaceAllString(name, " ")
	out = noiseToken.ReplaceAllString(out, " ")
	out = strings.TrimSpace(spacePattern.ReplaceAllString(out, " "))
	return out
}
