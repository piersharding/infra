package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/infrahq/infra/internal/logging"
	infraredis "github.com/infrahq/infra/internal/server/redis"
)

// CSRFTokenStore defines the interface for CSRF token storage
// This allows for different implementations (memory, Redis, etc.)
type CSRFTokenStore interface {
	// Add stores a new CSRF token with an expiration time
	Add(token string) error

	// Validate checks if a token exists and hasn't expired
	Validate(token string) bool

	// Remove deletes a token from the store
	Remove(token string) error

	// Close cleans up any resources used by the store
	Close() error
}

// CSRFStoreType represents the type of CSRF token store to use
type CSRFStoreType string

const (
	// CSRFStoreMemory uses in-memory storage (default)
	CSRFStoreMemory CSRFStoreType = "memory"

	// CSRFStoreRedis uses Redis for distributed storage
	CSRFStoreRedis CSRFStoreType = "redis"
)

// CSRFStoreConfig holds configuration for the CSRF token store
type CSRFStoreConfig struct {
	// Type specifies which store implementation to use
	// Default is "memory"
	Type CSRFStoreType

	// TokenExpiry is how long tokens are valid
	// Default is 24 hours
	TokenExpiry time.Duration

	// Redis client for Redis-backed storage
	// Only used when Type is CSRFStoreRedis
	Redis *infraredis.Redis
}

// DefaultCSRFStoreConfig returns the default CSRF store configuration
func DefaultCSRFStoreConfig() CSRFStoreConfig {
	return CSRFStoreConfig{
		Type:        CSRFStoreMemory,
		TokenExpiry: csrfTokenExpiry,
		Redis:       nil,
	}
}

// NewCSRFTokenStore creates a new CSRF token store based on the configuration
func NewCSRFTokenStore(config CSRFStoreConfig) (CSRFTokenStore, error) {
	if config.TokenExpiry == 0 {
		config.TokenExpiry = csrfTokenExpiry
	}

	switch config.Type {
	case CSRFStoreRedis:
		if config.Redis == nil {
			logging.L.Warn().Msg("Redis not configured for CSRF store, falling back to memory store")
			return newMemoryCSRFStore(config.TokenExpiry), nil
		}
		return newRedisCSRFStore(config.Redis, config.TokenExpiry)
	case CSRFStoreMemory, "":
		return newMemoryCSRFStore(config.TokenExpiry), nil
	default:
		return nil, fmt.Errorf("unknown CSRF store type: %s", config.Type)
	}
}

// memoryCSRFStore implements CSRFTokenStore using in-memory storage
// This is the default implementation suitable for single-instance deployments
type memoryCSRFStore struct {
	mu          sync.RWMutex
	tokens      map[string]time.Time
	tokenExpiry time.Duration
	stopCleanup chan struct{}
}

func newMemoryCSRFStore(tokenExpiry time.Duration) *memoryCSRFStore {
	store := &memoryCSRFStore{
		tokens:      make(map[string]time.Time),
		tokenExpiry: tokenExpiry,
		stopCleanup: make(chan struct{}),
	}
	// Start background cleanup goroutine
	go store.cleanup()
	return store
}

func (s *memoryCSRFStore) Add(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = time.Now().Add(s.tokenExpiry)
	return nil
}

func (s *memoryCSRFStore) Validate(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	expiry, exists := s.tokens[token]
	if !exists {
		return false
	}
	return time.Now().Before(expiry)
}

func (s *memoryCSRFStore) Remove(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
	return nil
}

func (s *memoryCSRFStore) Close() error {
	close(s.stopCleanup)
	return nil
}

func (s *memoryCSRFStore) cleanup() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for token, expiry := range s.tokens {
				if now.After(expiry) {
					delete(s.tokens, token)
				}
			}
			s.mu.Unlock()
		case <-s.stopCleanup:
			return
		}
	}
}

// redisCSRFStore implements CSRFTokenStore using Redis
// This is suitable for distributed deployments with multiple server instances
type redisCSRFStore struct {
	client      *redis.Client
	tokenExpiry time.Duration
	keyPrefix   string
}

func newRedisCSRFStore(r *infraredis.Redis, tokenExpiry time.Duration) (*redisCSRFStore, error) {
	client := r.Client()
	if client == nil {
		return nil, fmt.Errorf("Redis client is nil")
	}

	// Verify Redis connection
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis for CSRF store: %w", err)
	}

	store := &redisCSRFStore{
		client:      client,
		tokenExpiry: tokenExpiry,
		keyPrefix:   "csrf:",
	}

	logging.L.Info().Msg("Redis-backed CSRF token store initialized")
	return store, nil
}

func (s *redisCSRFStore) key(token string) string {
	return s.keyPrefix + token
}

func (s *redisCSRFStore) Add(token string) error {
	ctx := context.Background()
	return s.client.Set(ctx, s.key(token), "1", s.tokenExpiry).Err()
}

func (s *redisCSRFStore) Validate(token string) bool {
	ctx := context.Background()
	result, err := s.client.Exists(ctx, s.key(token)).Result()
	if err != nil {
		logging.L.Error().Err(err).Msg("failed to validate CSRF token in Redis")
		return false
	}
	return result > 0
}

func (s *redisCSRFStore) Remove(token string) error {
	ctx := context.Background()
	return s.client.Del(ctx, s.key(token)).Err()
}

func (s *redisCSRFStore) Close() error {
	// Redis client lifecycle is managed externally
	return nil
}

// RedisCSRFStoreWithClient creates a Redis-backed CSRF store with a provided Redis client
// This is the preferred method when you have direct access to the Redis client
func RedisCSRFStoreWithClient(client *redis.Client, tokenExpiry time.Duration) CSRFTokenStore {
	if tokenExpiry == 0 {
		tokenExpiry = csrfTokenExpiry
	}
	return &redisCSRFStore{
		client:      client,
		tokenExpiry: tokenExpiry,
		keyPrefix:   "csrf:",
	}
}
