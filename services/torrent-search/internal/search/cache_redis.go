package search

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"torrentstream/searchservice/internal/domain"
)

const redisCachePrefix = "tsearch:cache:"

// RedisCacheBackend stores search responses in Redis with JSON serialization.
type RedisCacheBackend struct {
	client *redis.Client
}

func NewRedisCacheBackend(client *redis.Client) *RedisCacheBackend {
	return &RedisCacheBackend{client: client}
}

func (r *RedisCacheBackend) Get(ctx context.Context, key string) (domain.SearchResponse, bool, error) {
	data, err := r.client.Get(ctx, redisCachePrefix+key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return domain.SearchResponse{}, false, nil
		}
		return domain.SearchResponse{}, false, err
	}
	var resp domain.SearchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return domain.SearchResponse{}, false, err
	}
	return resp, true, nil
}

func (r *RedisCacheBackend) Set(ctx context.Context, key string, response domain.SearchResponse, ttl time.Duration) error {
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, redisCachePrefix+key, data, ttl).Err()
}

func (r *RedisCacheBackend) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, redisCachePrefix+key).Err()
}

func (r *RedisCacheBackend) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}
