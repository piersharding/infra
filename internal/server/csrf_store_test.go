package server

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestMemoryCSRFStore(t *testing.T) {
	t.Run("Add and Validate", func(t *testing.T) {
		store := newMemoryCSRFStore(time.Hour)
		defer store.Close()

		token := "test-token-123"

		// Token should not exist initially
		assert.Assert(t, !store.Validate(token), "token should not exist before adding")

		// Add the token
		err := store.Add(token)
		assert.NilError(t, err)

		// Token should now be valid
		assert.Assert(t, store.Validate(token), "token should be valid after adding")
	})

	t.Run("Remove", func(t *testing.T) {
		store := newMemoryCSRFStore(time.Hour)
		defer store.Close()

		token := "test-token-remove"

		err := store.Add(token)
		assert.NilError(t, err)
		assert.Assert(t, store.Validate(token), "token should be valid after adding")

		// Remove the token
		err = store.Remove(token)
		assert.NilError(t, err)

		// Token should no longer be valid
		assert.Assert(t, !store.Validate(token), "token should not be valid after removal")
	})

	t.Run("Expiration", func(t *testing.T) {
		// Use a very short expiry for testing
		store := newMemoryCSRFStore(50 * time.Millisecond)
		defer store.Close()

		token := "test-token-expiry"

		err := store.Add(token)
		assert.NilError(t, err)
		assert.Assert(t, store.Validate(token), "token should be valid immediately after adding")

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		// Token should now be expired
		assert.Assert(t, !store.Validate(token), "token should be invalid after expiry")
	})

	t.Run("Multiple Tokens", func(t *testing.T) {
		store := newMemoryCSRFStore(time.Hour)
		defer store.Close()

		tokens := []string{"token-1", "token-2", "token-3"}

		// Add all tokens
		for _, token := range tokens {
			err := store.Add(token)
			assert.NilError(t, err)
		}

		// All tokens should be valid
		for _, token := range tokens {
			assert.Assert(t, store.Validate(token), "token %s should be valid", token)
		}

		// Remove one token
		err := store.Remove(tokens[1])
		assert.NilError(t, err)

		// First and third should still be valid
		assert.Assert(t, store.Validate(tokens[0]), "token[0] should still be valid")
		assert.Assert(t, !store.Validate(tokens[1]), "token[1] should be invalid after removal")
		assert.Assert(t, store.Validate(tokens[2]), "token[2] should still be valid")
	})

	t.Run("Validate Non-Existent Token", func(t *testing.T) {
		store := newMemoryCSRFStore(time.Hour)
		defer store.Close()

		assert.Assert(t, !store.Validate("non-existent-token"), "non-existent token should not be valid")
	})

	t.Run("Remove Non-Existent Token", func(t *testing.T) {
		store := newMemoryCSRFStore(time.Hour)
		defer store.Close()

		// Should not error when removing non-existent token
		err := store.Remove("non-existent-token")
		assert.NilError(t, err)
	})

	t.Run("Close", func(t *testing.T) {
		store := newMemoryCSRFStore(time.Hour)

		err := store.Close()
		assert.NilError(t, err)

		// Should still be able to use the store after close (just cleanup goroutine stops)
		err = store.Add("test-token")
		assert.NilError(t, err)
	})
}

func TestNewCSRFTokenStore(t *testing.T) {
	t.Run("Default Memory Store", func(t *testing.T) {
		config := DefaultCSRFStoreConfig()
		store, err := NewCSRFTokenStore(config)
		assert.NilError(t, err)
		assert.Assert(t, store != nil, "store should not be nil")
		defer store.Close()

		// Verify it works
		err = store.Add("test-token")
		assert.NilError(t, err)
		assert.Assert(t, store.Validate("test-token"))
	})

	t.Run("Empty Type Defaults To Memory", func(t *testing.T) {
		config := CSRFStoreConfig{
			Type:        "", // Empty should default to memory
			TokenExpiry: time.Hour,
		}
		store, err := NewCSRFTokenStore(config)
		assert.NilError(t, err)
		assert.Assert(t, store != nil, "store should not be nil")
		defer store.Close()
	})

	t.Run("Redis Without Client Falls Back To Memory", func(t *testing.T) {
		config := CSRFStoreConfig{
			Type:        CSRFStoreRedis,
			TokenExpiry: time.Hour,
			Redis:       nil, // No Redis client
		}
		store, err := NewCSRFTokenStore(config)
		assert.NilError(t, err)
		assert.Assert(t, store != nil, "store should not be nil (fallback to memory)")
		defer store.Close()

		// Verify it works (should be memory store)
		err = store.Add("test-token")
		assert.NilError(t, err)
		assert.Assert(t, store.Validate("test-token"))
	})

	t.Run("Unknown Type Returns Error", func(t *testing.T) {
		config := CSRFStoreConfig{
			Type:        "unknown-type",
			TokenExpiry: time.Hour,
		}
		store, err := NewCSRFTokenStore(config)
		assert.ErrorContains(t, err, "unknown CSRF store type")
		assert.Assert(t, store == nil, "store should be nil on error")
	})

	t.Run("Zero TokenExpiry Uses Default", func(t *testing.T) {
		config := CSRFStoreConfig{
			Type:        CSRFStoreMemory,
			TokenExpiry: 0, // Should use default
		}
		store, err := NewCSRFTokenStore(config)
		assert.NilError(t, err)
		assert.Assert(t, store != nil)
		defer store.Close()

		// Verify the store works with default expiry
		err = store.Add("test-token")
		assert.NilError(t, err)
		assert.Assert(t, store.Validate("test-token"))
	})
}

func TestDefaultCSRFStoreConfig(t *testing.T) {
	config := DefaultCSRFStoreConfig()

	assert.Equal(t, config.Type, CSRFStoreMemory)
	assert.Equal(t, config.TokenExpiry, csrfTokenExpiry)
	assert.Assert(t, config.Redis == nil)
}

func TestCSRFStoreType(t *testing.T) {
	// Verify the constant values
	assert.Equal(t, string(CSRFStoreMemory), "memory")
	assert.Equal(t, string(CSRFStoreRedis), "redis")
}

// TestMemoryCSRFStoreConcurrency tests concurrent access to the memory store
func TestMemoryCSRFStoreConcurrency(t *testing.T) {
	store := newMemoryCSRFStore(time.Hour)
	defer store.Close()

	// Run concurrent operations
	done := make(chan bool, 100)

	// Concurrent adds
	for i := 0; i < 50; i++ {
		go func(id int) {
			token := "concurrent-token-" + string(rune('A'+id))
			err := store.Add(token)
			if err != nil {
				t.Errorf("concurrent add failed: %v", err)
			}
			done <- true
		}(i)
	}

	// Concurrent validates
	for i := 0; i < 50; i++ {
		go func(id int) {
			token := "concurrent-token-" + string(rune('A'+id))
			_ = store.Validate(token) // May or may not exist yet
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}

	// Verify all tokens exist
	for i := 0; i < 50; i++ {
		token := "concurrent-token-" + string(rune('A'+i))
		assert.Assert(t, store.Validate(token), "token %s should exist after concurrent operations", token)
	}
}
