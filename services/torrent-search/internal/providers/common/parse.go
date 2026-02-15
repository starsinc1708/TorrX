package common

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

var tagPattern = regexp.MustCompile(`<[^>]+>`)

func CleanHTMLText(raw string) string {
	value := strings.TrimSpace(raw)
	value = html.UnescapeString(value)
	value = tagPattern.ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func ParseHumanSize(raw string) int64 {
	value := strings.TrimSpace(strings.ToUpper(raw))
	value = strings.ReplaceAll(value, "ГБ", "GB")
	value = strings.ReplaceAll(value, "МБ", "MB")
	value = strings.ReplaceAll(value, "КБ", "KB")
	value = strings.ReplaceAll(value, "ТБ", "TB")
	value = strings.ReplaceAll(value, "Б", "B")
	if value == "" {
		return 0
	}

	unit := ""
	number := value
	for _, suffix := range []string{"TB", "GB", "MB", "KB", "B"} {
		if strings.HasSuffix(number, suffix) {
			unit = suffix
			number = strings.TrimSpace(strings.TrimSuffix(number, suffix))
			break
		}
	}
	if unit == "" {
		if parsed, err := strconv.ParseInt(number, 10, 64); err == nil {
			return parsed
		}
		return 0
	}

	parsed, err := strconv.ParseFloat(strings.ReplaceAll(number, ",", "."), 64)
	if err != nil || parsed < 0 {
		return 0
	}

	multiplier := float64(1)
	switch unit {
	case "KB":
		multiplier = 1024
	case "MB":
		multiplier = 1024 * 1024
	case "GB":
		multiplier = 1024 * 1024 * 1024
	case "TB":
		multiplier = 1024 * 1024 * 1024 * 1024
	}
	return int64(parsed * multiplier)
}
