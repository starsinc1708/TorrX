package search

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"torrentstream/searchservice/internal/domain"
	"torrentstream/searchservice/internal/metrics"
)

const (
	providerFailureThreshold = 3
	providerBlockBase        = 2 * time.Minute
	providerBlockMax         = 15 * time.Minute
)

type providerHealth struct {
	consecutiveFailures int
	blockedUntil        time.Time
	lastError           string
	lastSuccessAt       time.Time
	lastFailureAt       time.Time
	lastLatency         time.Duration
	lastTimeout         bool
	lastQuery           string
	totalRequests       int64
	totalFailures       int64
	timeoutCount        int64
}

func (s *Service) isProviderBlocked(providerName string, now time.Time) (bool, time.Time, string) {
	if s == nil {
		return false, time.Time{}, ""
	}
	name := strings.ToLower(strings.TrimSpace(providerName))
	if name == "" {
		return false, time.Time{}, ""
	}

	s.healthMu.Lock()
	defer s.healthMu.Unlock()

	state := s.health[name]
	if state == nil {
		return false, time.Time{}, ""
	}
	if state.blockedUntil.IsZero() || now.After(state.blockedUntil) {
		return false, time.Time{}, ""
	}
	return true, state.blockedUntil, state.lastError
}

func (s *Service) recordProviderResult(providerName, query string, err error, latency time.Duration, now time.Time) {
	if s == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(providerName))
	if name == "" {
		return
	}

	s.healthMu.Lock()
	defer s.healthMu.Unlock()

	state := s.health[name]
	if state == nil {
		state = &providerHealth{}
		s.health[name] = state
	}
	state.totalRequests++
	state.lastQuery = strings.TrimSpace(query)
	if latency > 0 {
		state.lastLatency = latency
		metrics.ProviderRequestDuration.WithLabelValues(name).Observe(latency.Seconds())
	}
	state.lastTimeout = isTimeoutLikeError(err)
	if state.lastTimeout {
		state.timeoutCount++
	}

	if err == nil {
		state.consecutiveFailures = 0
		state.blockedUntil = time.Time{}
		state.lastError = ""
		state.lastSuccessAt = now
		metrics.ProviderRequestsTotal.WithLabelValues(name, "ok").Inc()
		metrics.ProviderAvailable.WithLabelValues(name).Set(1)
		return
	}

	state.consecutiveFailures++
	state.totalFailures++
	state.lastFailureAt = now
	state.lastError = err.Error()

	status := "error"
	if state.lastTimeout {
		status = "timeout"
	}
	metrics.ProviderRequestsTotal.WithLabelValues(name, status).Inc()

	if state.consecutiveFailures >= providerFailureThreshold {
		state.blockedUntil = now.Add(exponentialBlockDuration(state.consecutiveFailures))
		metrics.ProviderAvailable.WithLabelValues(name).Set(0)
	}
}

// exponentialBlockDuration calculates how long to block a provider based on
// consecutive failures: baseDuration × 2^(failures - threshold), capped at 15min.
func exponentialBlockDuration(consecutiveFailures int) time.Duration {
	exponent := consecutiveFailures - providerFailureThreshold
	if exponent < 0 {
		exponent = 0
	}
	d := providerBlockBase
	for i := 0; i < exponent; i++ {
		d *= 2
		if d > providerBlockMax {
			return providerBlockMax
		}
	}
	return d
}

func isTimeoutLikeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "timeout") || strings.Contains(value, "deadline exceeded")
}

func (s *Service) ProviderDiagnostics() []domain.ProviderDiagnostics {
	infos := s.Providers()
	if len(infos) == 0 {
		return nil
	}

	s.healthMu.Lock()
	defer s.healthMu.Unlock()

	// Build a name→provider lookup so we can check for IndexerLister.
	providerByName := make(map[string]Provider, len(s.providers))
	for _, p := range s.providers {
		key := strings.ToLower(strings.TrimSpace(p.Name()))
		if key != "" {
			providerByName[key] = p
		}
	}

	items := make([]domain.ProviderDiagnostics, 0, len(infos))
	for _, info := range infos {
		name := strings.ToLower(strings.TrimSpace(info.Name))
		state := s.health[name]
		item := domain.ProviderDiagnostics{
			Name:    info.Name,
			Label:   info.Label,
			Kind:    info.Kind,
			Enabled: info.Enabled,
		}
		if p, ok := providerByName[name]; ok {
			if il, ok := p.(IndexerLister); ok {
				item.FanOut = il.FanOutActive()
				item.SubIndexers = il.ListSubIndexers()
			}
		}
		if state != nil {
			item.ConsecutiveFailures = state.consecutiveFailures
			if !state.blockedUntil.IsZero() {
				blockedUntil := state.blockedUntil
				item.BlockedUntil = &blockedUntil
			}
			item.LastError = state.lastError
			if !state.lastSuccessAt.IsZero() {
				lastSuccessAt := state.lastSuccessAt
				item.LastSuccessAt = &lastSuccessAt
			}
			if !state.lastFailureAt.IsZero() {
				lastFailureAt := state.lastFailureAt
				item.LastFailureAt = &lastFailureAt
			}
			item.LastLatencyMS = state.lastLatency.Milliseconds()
			item.LastTimeout = state.lastTimeout
			item.LastQuery = state.lastQuery
			item.TotalRequests = state.totalRequests
			item.TotalFailures = state.totalFailures
			item.TimeoutCount = state.timeoutCount
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}
