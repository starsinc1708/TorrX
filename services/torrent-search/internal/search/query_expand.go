package search

import (
	"strings"

	"torrentstream/searchservice/internal/domain"
)

var ruQueryTokens = map[string]struct{}{
	"ru":      {},
	"rus":     {},
	"russian": {},
	"рус":     {},
}

func expandedQueryForProvider(query, providerName string, profile domain.SearchRankingProfile) string {
	base := strings.TrimSpace(query)
	if base == "" {
		return base
	}

	if !prefersAnyToken(profile.PreferredAudio, ruQueryTokens) && !prefersAnyToken(profile.PreferredSubtitles, ruQueryTokens) {
		return base
	}

	name := strings.ToLower(strings.TrimSpace(providerName))

	// RuTracker is already Russian-focused, no expansion needed.
	if name == "rutracker" {
		return base
	}

	if hasAnyToken(base, ruQueryTokens) {
		return base
	}

	// For 1337x, try transliteration + "russian" for Cyrillic queries.
	if name == "1337x" || name == "x1337" {
		if hasCyrillic(base) {
			translit := transliterateCyrillic(base)
			if translit != "" && translit != base {
				return translit + " russian"
			}
		}
		return base + " russian"
	}

	// For PirateBay/DHT and others, append "rus".
	return base + " rus"
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if r >= 0x0400 && r <= 0x04FF {
			return true
		}
	}
	return false
}

func prefersAnyToken(values []string, tokens map[string]struct{}) bool {
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" {
			continue
		}
		if _, ok := tokens[key]; ok {
			return true
		}
	}
	return false
}

func hasAnyToken(input string, tokens map[string]struct{}) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	normalized = strings.ReplaceAll(normalized, "\u0451", "\u0435")
	for _, token := range tokenPattern.FindAllString(normalized, -1) {
		if _, ok := tokens[token]; ok {
			return true
		}
	}
	return false
}
