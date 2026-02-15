package common

import (
	"net/url"
	"strings"
)

func NormalizeInfoHash(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(strings.ToLower(value), "urn:btih:")
	if value == "" {
		return ""
	}
	return value
}

func BuildMagnet(infoHash, name string, trackers []string) string {
	hash := NormalizeInfoHash(infoHash)
	if hash == "" {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("magnet:?xt=urn:btih:")
	builder.WriteString(hash)
	if strings.TrimSpace(name) != "" {
		builder.WriteString("&dn=")
		builder.WriteString(url.QueryEscape(strings.TrimSpace(name)))
	}
	for _, tracker := range trackers {
		value := strings.TrimSpace(tracker)
		if value == "" {
			continue
		}
		builder.WriteString("&tr=")
		builder.WriteString(url.QueryEscape(value))
	}
	return builder.String()
}
