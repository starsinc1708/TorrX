package search

import (
	"regexp"
	"strings"

	"torrentstream/searchservice/internal/domain"
)

// knownDubbingGroups maps lowercase variants of dubbing group names to their canonical form.
var knownDubbingGroups = map[string]string{
	"lostfilm":      "LostFilm",
	"lost film":     "LostFilm",
	"newstudio":     "NewStudio",
	"new studio":    "NewStudio",
	"кубик в кубе":  "Кубик в Кубе",
	"кубиквкубе":    "Кубик в Кубе",
	"kubikvkube":    "Кубик в Кубе",
	"baibako":       "BaibaKo",
	"байбако":       "BaibaKo",
	"coldfilm":      "ColdFilm",
	"cold film":     "ColdFilm",
	"колдфилм":      "ColdFilm",
	"hdrezka":       "HDRezka",
	"hd rezka":      "HDRezka",
	"резка":         "HDRezka",
	"amedia":        "Amedia",
	"амедиа":        "Amedia",
	"ideafilm":      "IdeaFilm",
	"idea film":     "IdeaFilm",
	"идеафилм":      "IdeaFilm",
	"novafilm":      "NovaFilm",
	"nova film":     "NovaFilm",
	"новафилм":      "NovaFilm",
	"alexfilm":      "AlexFilm",
	"алексфилм":     "AlexFilm",
	"kerob":         "Kerob",
	"gears media":   "Gears Media",
	"gearsmedia":    "Gears Media",
	"profix media":  "Profix Media",
	"profixmedia":   "Profix Media",
	"jaskier":       "Jaskier",
	"яскиер":        "Jaskier",
	"omskbird":      "OmskBird",
	"smarty":        "Smarty",
	"hamster":       "Hamster",
	"хамстер":       "Hamster",
	"sdi media":     "SDI Media",
	"sdamzavas":     "SDI Media",
	"tvshows":       "TVShows",
	"tv shows":      "TVShows",
	"невафильм":     "НеsaFilm",
	"дублинг":       "Дублинг",
	"vo":            "VO",
	"кураж бамбей":  "Кураж-Бамбей",
	"кураж-бамбей":  "Кураж-Бамбей",
	"kuraj bambey":  "Кураж-Бамбей",
	"кравец":        "Кравец",
	"живов":         "Живов",
	"сербин":        "Сербин",
	"визгунов":      "Визгунов",
	"гаврилов":      "Гаврилов",
	"володарский":   "Володарский",
	"пучков":        "Пучков",
	"goblin":        "Пучков",
}

var dubbingTypePatterns = []struct {
	pattern *regexp.Regexp
	dtype   domain.DubbingType
}{
	{regexp.MustCompile(`(?i)(?:проф(?:\.|ессиональн[а-яё]*)?\s*)?дубляж`), domain.DubbingDub},
	{regexp.MustCompile(`(?i)(?:двух|2-?х?)\s*голос`), domain.DubbingMultiVoice},
	{regexp.MustCompile(`(?i)многоголос`), domain.DubbingMultiVoice},
	{regexp.MustCompile(`(?i)(?:одноголос|авторск)`), domain.DubbingAuthor},
	{regexp.MustCompile(`(?i)закадров`), domain.DubbingBackVoice},
	{regexp.MustCompile(`(?i)озвуч`), domain.DubbingVoiceover},
	{regexp.MustCompile(`(?i)(?:^|\W)dub(?:bed)?(?:\W|$)`), domain.DubbingDub},
}

// professionalGroups are groups that provide professional-quality dubbing.
var professionalGroups = map[string]bool{
	"LostFilm":     true,
	"NewStudio":    true,
	"Amedia":       true,
	"SDI Media":    true,
	"Кубик в Кубе": true,
	"TVShows":      true,
}

func detectDubbing(raw string) domain.DubbingInfo {
	input := strings.ToLower(strings.TrimSpace(raw))
	input = strings.ReplaceAll(input, "ё", "е")
	if input == "" {
		return domain.DubbingInfo{}
	}

	info := domain.DubbingInfo{}

	// Detect dubbing groups
	seen := make(map[string]bool)
	for key, canonical := range knownDubbingGroups {
		if strings.Contains(input, key) {
			if !seen[canonical] {
				seen[canonical] = true
				info.Groups = append(info.Groups, canonical)
			}
		}
	}
	if len(info.Groups) > 0 {
		info.Group = info.Groups[0]
	}

	// Detect dubbing type
	for _, entry := range dubbingTypePatterns {
		if entry.pattern.MatchString(input) {
			info.Type = entry.dtype
			break
		}
	}

	// If we found a known professional group but no explicit type, assume dubbing
	if info.Type == domain.DubbingUnknown && len(info.Groups) > 0 {
		for _, g := range info.Groups {
			if professionalGroups[g] {
				info.Type = domain.DubbingDub
				break
			}
		}
		if info.Type == domain.DubbingUnknown {
			info.Type = domain.DubbingVoiceover
		}
	}

	return info
}

func dubbingScore(info domain.DubbingInfo) float64 {
	score := 0.0

	// Bonus for known dubbing group
	if len(info.Groups) > 0 {
		score += 5
		for _, g := range info.Groups {
			if professionalGroups[g] {
				score += 2
				break
			}
		}
	}

	// Bonus by dubbing type
	switch info.Type {
	case domain.DubbingDub:
		score += 4
	case domain.DubbingMultiVoice:
		score += 3
	case domain.DubbingVoiceover:
		score += 2
	case domain.DubbingAuthor:
		score += 1
	case domain.DubbingBackVoice:
		score += 1
	}

	return score
}
