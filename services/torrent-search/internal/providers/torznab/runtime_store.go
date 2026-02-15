package torznab

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

const defaultRuntimeConfigStoreKey = "search:providers:runtime:v1"

type RuntimeProviderState struct {
	Endpoint string `json:"endpoint,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
	ProxyURL string `json:"proxyUrl,omitempty"`
}

type RuntimeConfigStore interface {
	Load(ctx context.Context) (map[string]RuntimeProviderState, error)
	Save(ctx context.Context, provider string, state RuntimeProviderState) error
}

type RedisRuntimeConfigStore struct {
	client redis.UniversalClient
	key    string
}

func NewRedisRuntimeConfigStore(client redis.UniversalClient, key string) *RedisRuntimeConfigStore {
	if client == nil {
		return nil
	}
	storeKey := strings.TrimSpace(key)
	if storeKey == "" {
		storeKey = defaultRuntimeConfigStoreKey
	}
	return &RedisRuntimeConfigStore{
		client: client,
		key:    storeKey,
	}
}

func (s *RedisRuntimeConfigStore) Load(ctx context.Context) (map[string]RuntimeProviderState, error) {
	if s == nil || s.client == nil {
		return nil, nil
	}
	items, err := s.client.HGetAll(ctx, s.key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}

	out := make(map[string]RuntimeProviderState, len(items))
	for provider, encoded := range items {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" || strings.TrimSpace(encoded) == "" {
			continue
		}
		var state RuntimeProviderState
		if err := json.Unmarshal([]byte(encoded), &state); err != nil {
			continue
		}
		out[name] = state
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (s *RedisRuntimeConfigStore) Save(ctx context.Context, provider string, state RuntimeProviderState) error {
	if s == nil || s.client == nil {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" {
		return nil
	}
	state.Endpoint = strings.TrimSpace(state.Endpoint)
	state.APIKey = strings.TrimSpace(state.APIKey)
	state.ProxyURL = strings.TrimSpace(state.ProxyURL)

	if state.Endpoint == "" && state.APIKey == "" && state.ProxyURL == "" {
		return s.client.HDel(ctx, s.key, name).Err()
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.client.HSet(ctx, s.key, name, payload).Err()
}
