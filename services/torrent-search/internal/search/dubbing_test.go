package search

import (
	"testing"

	"torrentstream/searchservice/internal/domain"
)

// ---------------------------------------------------------------------------
// detectDubbing — type detection
// ---------------------------------------------------------------------------

func TestDetectDubbingProfessionalDub(t *testing.T) {
	info := detectDubbing("Фильм 2024 профессиональный дубляж")
	if info.Type != domain.DubbingDub {
		t.Fatalf("expected DubbingDub, got %v", info.Type)
	}
}

func TestDetectDubbingMultiVoice(t *testing.T) {
	info := detectDubbing("Фильм 2024 многоголосый перевод")
	if info.Type != domain.DubbingMultiVoice {
		t.Fatalf("expected DubbingMultiVoice, got %v", info.Type)
	}
}

func TestDetectDubbingTwoVoice(t *testing.T) {
	info := detectDubbing("Фильм двухголосый")
	if info.Type != domain.DubbingMultiVoice {
		t.Fatalf("expected DubbingMultiVoice for двухголосый, got %v", info.Type)
	}
}

func TestDetectDubbingAuthor(t *testing.T) {
	info := detectDubbing("Фильм авторский перевод")
	if info.Type != domain.DubbingAuthor {
		t.Fatalf("expected DubbingAuthor, got %v", info.Type)
	}
}

func TestDetectDubbingBackVoice(t *testing.T) {
	info := detectDubbing("Фильм закадровый")
	if info.Type != domain.DubbingBackVoice {
		t.Fatalf("expected DubbingBackVoice, got %v", info.Type)
	}
}

func TestDetectDubbingVoiceover(t *testing.T) {
	info := detectDubbing("Фильм озвучка")
	if info.Type != domain.DubbingVoiceover {
		t.Fatalf("expected DubbingVoiceover, got %v", info.Type)
	}
}

func TestDetectDubbingEnglishDub(t *testing.T) {
	info := detectDubbing("Movie 2024 dubbed")
	if info.Type != domain.DubbingDub {
		t.Fatalf("expected DubbingDub for 'dubbed', got %v", info.Type)
	}
}

func TestDetectDubbingEmpty(t *testing.T) {
	info := detectDubbing("")
	if info.Type != domain.DubbingUnknown {
		t.Fatalf("expected DubbingUnknown for empty input, got %v", info.Type)
	}
	if len(info.Groups) != 0 {
		t.Fatalf("expected no groups, got %v", info.Groups)
	}
}

func TestDetectDubbingNoMatch(t *testing.T) {
	info := detectDubbing("Plain movie 1080p")
	if info.Type != domain.DubbingUnknown {
		t.Fatalf("expected DubbingUnknown for plain title, got %v", info.Type)
	}
}

// ---------------------------------------------------------------------------
// detectDubbing — group detection
// ---------------------------------------------------------------------------

func TestDetectDubbingGroupLostFilm(t *testing.T) {
	info := detectDubbing("Show S01E01 LostFilm 1080p")
	if len(info.Groups) == 0 || info.Groups[0] != "LostFilm" {
		t.Fatalf("expected LostFilm group, got %v", info.Groups)
	}
	if info.Group != "LostFilm" {
		t.Fatalf("expected Group=LostFilm, got %s", info.Group)
	}
}

func TestDetectDubbingGroupNewStudio(t *testing.T) {
	info := detectDubbing("Show NewStudio перевод")
	found := false
	for _, g := range info.Groups {
		if g == "NewStudio" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected NewStudio group, got %v", info.Groups)
	}
}

func TestDetectDubbingGroupKurazBambey(t *testing.T) {
	info := detectDubbing("Show кураж-бамбей 1080p")
	found := false
	for _, g := range info.Groups {
		if g == "Кураж-Бамбей" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Кураж-Бамбей group, got %v", info.Groups)
	}
}

func TestDetectDubbingGroupGoblinMapsToПучков(t *testing.T) {
	info := detectDubbing("Movie goblin translation")
	found := false
	for _, g := range info.Groups {
		if g == "Пучков" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Пучков (goblin alias), got %v", info.Groups)
	}
}

func TestDetectDubbingMultipleGroups(t *testing.T) {
	info := detectDubbing("Show LostFilm NewStudio 1080p")
	if len(info.Groups) < 2 {
		t.Fatalf("expected at least 2 groups, got %v", info.Groups)
	}
}

func TestDetectDubbingProfessionalGroupImpliesDub(t *testing.T) {
	// LostFilm is in professionalGroups, so type should be DubbingDub even without explicit dub keyword
	info := detectDubbing("Show LostFilm 1080p")
	if info.Type != domain.DubbingDub {
		t.Fatalf("expected DubbingDub for professional group, got %v", info.Type)
	}
}

func TestDetectDubbingNonProfessionalGroupImpliesVoiceover(t *testing.T) {
	// HDRezka is not in professionalGroups
	info := detectDubbing("Movie HDRezka перевод")
	if info.Type == domain.DubbingUnknown {
		t.Fatalf("expected non-unknown type for known group, got %v", info.Type)
	}
}

// ---------------------------------------------------------------------------
// dubbingScore
// ---------------------------------------------------------------------------

func TestDubbingScoreOrdering(t *testing.T) {
	dubScore := dubbingScore(domain.DubbingInfo{
		Type:   domain.DubbingDub,
		Groups: []string{"LostFilm"},
		Group:  "LostFilm",
	})
	voiceoverScore := dubbingScore(domain.DubbingInfo{
		Type:   domain.DubbingVoiceover,
		Groups: []string{"SomeGroup"},
		Group:  "SomeGroup",
	})
	unknownScore := dubbingScore(domain.DubbingInfo{})

	if dubScore <= voiceoverScore {
		t.Fatalf("expected dub > voiceover, got %.2f <= %.2f", dubScore, voiceoverScore)
	}
	if voiceoverScore <= unknownScore {
		t.Fatalf("expected voiceover > unknown, got %.2f <= %.2f", voiceoverScore, unknownScore)
	}
}

func TestDubbingScoreProfessionalBonus(t *testing.T) {
	professional := dubbingScore(domain.DubbingInfo{
		Type:   domain.DubbingDub,
		Groups: []string{"LostFilm"},
		Group:  "LostFilm",
	})
	nonProfessional := dubbingScore(domain.DubbingInfo{
		Type:   domain.DubbingDub,
		Groups: []string{"SomeGroup"},
		Group:  "SomeGroup",
	})
	if professional <= nonProfessional {
		t.Fatalf("expected professional group bonus, got %.2f <= %.2f", professional, nonProfessional)
	}
}

func TestDubbingScoreEmpty(t *testing.T) {
	score := dubbingScore(domain.DubbingInfo{})
	if score != 0 {
		t.Fatalf("expected 0 for empty dubbing info, got %.2f", score)
	}
}
