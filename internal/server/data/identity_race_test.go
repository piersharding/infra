package data

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

func TestUpdateIdentityLastSeenAt_RaceConditionPrevention(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		tx := txnForTestCase(t, db, db.DefaultOrg.ID)

		user := &models.Identity{Name: "test-user-race"}
		err := CreateIdentity(tx, user)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		// Test concurrent updates
		numGoroutines := 10
		var wg sync.WaitGroup
		var errors []error
		var mu sync.Mutex

		// Set initial LastSeenAt to ensure it's older than threshold
		err = UpdateIdentityLastSeenAt(db, user)
		require.NoError(t, err)

		// Reset to old time to ensure updates will happen
		oldTime := time.Now().Add(-5 * lastSeenUpdateThreshold)
		user.LastSeenAt = oldTime

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				err := UpdateIdentityLastSeenAt(db, user)
				mu.Lock()
				if err != nil {
					errors = append(errors, err)
				}
				mu.Unlock()
			}(i)
		}

		wg.Wait()

		// Check that no errors occurred
		if len(errors) > 0 {
			t.Errorf("Expected no errors, got %d: %v", len(errors), errors)
		}

		// Verify that the user was updated
		updatedUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)

		// The LastSeenAt should be updated and not the old time
		assert.NotEqual(t, oldTime, updatedUser.LastSeenAt)
		assert.True(t, updatedUser.LastSeenAt.After(oldTime))
	})
}

func TestUpdateIdentityLastSeenAt_Throttling(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		tx := txnForTestCase(t, db, db.DefaultOrg.ID)

		user := &models.Identity{Name: "test-user-throttle"}
		err := CreateIdentity(tx, user)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		// First update should succeed
		err = UpdateIdentityLastSeenAt(db, user)
		require.NoError(t, err)

		// Get the updated user
		updatedUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)
		firstUpdate := updatedUser.LastSeenAt

		// Second update immediately should be throttled (no error, but no update)
		err = UpdateIdentityLastSeenAt(db, updatedUser)
		require.NoError(t, err)

		// Get the user again to verify LastSeenAt wasn't updated
		secondUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)

		// LastSeenAt should be the same
		assert.Equal(t, firstUpdate, secondUser.LastSeenAt)
	})
}

func TestUpdateIdentityLastSeenAt_ThrottlingAfterDelay(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		tx := txnForTestCase(t, db, db.DefaultOrg.ID)

		user := &models.Identity{Name: "test-user-delay"}
		err := CreateIdentity(tx, user)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		// First update
		err = UpdateIdentityLastSeenAt(db, user)
		require.NoError(t, err)

		// Get the updated time
		updatedUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)
		firstUpdate := updatedUser.LastSeenAt

		// Wait for the throttling period to pass
		time.Sleep(lastSeenUpdateThreshold + 100*time.Millisecond)

		// Second update should now succeed
		err = UpdateIdentityLastSeenAt(db, updatedUser)
		require.NoError(t, err)

		// Get the user again
		secondUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)

		// LastSeenAt should be different (newer)
		assert.NotEqual(t, firstUpdate, secondUser.LastSeenAt)
		assert.True(t, secondUser.LastSeenAt.After(firstUpdate))
	})
}

func TestUpdateIdentityLastSeenAt_ConcurrentWithThrottling(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		tx := txnForTestCase(t, db, db.DefaultOrg.ID)

		user := &models.Identity{Name: "test-user-concurrent"}
		err := CreateIdentity(tx, user)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		// Initial update to set a baseline
		err = UpdateIdentityLastSeenAt(db, user)
		require.NoError(t, err)

		// Get the current time
		currentUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)
		baselineTime := currentUser.LastSeenAt

		// Wait a bit but less than throttling threshold
		time.Sleep(lastSeenUpdateThreshold / 2)

		// Try concurrent updates - should all be throttled
		numGoroutines := 5
		var wg sync.WaitGroup
		var errors []error
		var mu sync.Mutex

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := UpdateIdentityLastSeenAt(db, currentUser)
				mu.Lock()
				if err != nil {
					errors = append(errors, err)
				}
				mu.Unlock()
			}()
		}

		wg.Wait()

		// Check for errors
		if len(errors) > 0 {
			t.Errorf("Expected no errors, got %d: %v", len(errors), errors)
		}

		// Verify that LastSeenAt hasn't changed
		finalUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)
		assert.Equal(t, baselineTime, finalUser.LastSeenAt)
	})
}

func TestUpdateIdentityLastSeenAt_DatabaseLockHandling(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		tx := txnForTestCase(t, db, db.DefaultOrg.ID)

		user := &models.Identity{Name: "test-user-lock"}
		err := CreateIdentity(tx, user)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		// Set initial time to ensure updates will happen
		initialTime := time.Now().Add(-5 * lastSeenUpdateThreshold)
		user.LastSeenAt = initialTime

		// Start a transaction that holds a lock
		lockTx, err := db.Begin(context.Background(), nil)
		require.NoError(t, err)
		defer lockTx.Rollback()

		// Update the user in the transaction to create a lock
		err = UpdateIdentityLastSeenAt(lockTx.WithOrgID(db.DefaultOrg.ID), user)
		require.NoError(t, err)

		// In a separate goroutine, try to update the same user
		// This should either wait for the lock or be throttled appropriately
		var wg sync.WaitGroup
		var updateErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Use a copy with old time to avoid race
			userCopy := &models.Identity{
				Model:              user.Model,
				OrganizationMember: user.OrganizationMember,
				Name:               user.Name,
				LastSeenAt:         initialTime,
			}
			updateErr = UpdateIdentityLastSeenAt(db, userCopy)
		}()

		// Commit the transaction after a short delay
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = lockTx.Commit()
		}()

		wg.Wait()

		// The concurrent update should succeed
		require.NoError(t, updateErr)

		// Verify that the update happened
		finalUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)
		assert.True(t, finalUser.LastSeenAt.After(initialTime),
			"finalUser.LastSeenAt %v should be after initialTime %v",
			finalUser.LastSeenAt, initialTime)
	})
}

func TestUpdateIdentityLastSeenAt_AdvisoryLockCleanup(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		tx := txnForTestCase(t, db, db.DefaultOrg.ID)

		user := &models.Identity{Name: "test-user-cleanup"}
		err := CreateIdentity(tx, user)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		// Set initial time to ensure update will happen
		initialTime := time.Now().Add(-5 * lastSeenUpdateThreshold)
		user.LastSeenAt = initialTime

		// Perform update
		err = UpdateIdentityLastSeenAt(db, user)
		require.NoError(t, err)

		// Verify that the update succeeded
		updatedUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)
		assert.True(t, updatedUser.LastSeenAt.After(initialTime),
			"updatedUser.LastSeenAt %v should be after initialTime %v",
			updatedUser.LastSeenAt, initialTime)

		// Capture the time after first update
		firstUpdateTime := updatedUser.LastSeenAt

		// Test that subsequent updates work (locks were properly released)
		time.Sleep(lastSeenUpdateThreshold + 100*time.Millisecond)

		// Need to set LastSeenAt on updatedUser to old time to bypass throttle check
		updatedUser.LastSeenAt = time.Now().Add(-5 * lastSeenUpdateThreshold)
		err = UpdateIdentityLastSeenAt(db, updatedUser)
		require.NoError(t, err)

		finalUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
		require.NoError(t, err)
		assert.True(t, finalUser.LastSeenAt.After(firstUpdateTime),
			"finalUser.LastSeenAt %v should be after firstUpdateTime %v",
			finalUser.LastSeenAt, firstUpdateTime)
	})
}

func TestUpdateIdentityLastSeenAt_MultipleUsersConcurrent(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		tx := txnForTestCase(t, db, db.DefaultOrg.ID)

		// Create multiple users
		var users []*models.Identity
		for i := 0; i < 5; i++ {
			user := &models.Identity{Name: fmt.Sprintf("multi-user-%d", i)}
			err := CreateIdentity(tx, user)
			require.NoError(t, err)
			users = append(users, user)
		}
		require.NoError(t, tx.Commit())

		// Capture the initial LastSeenAt times before concurrent updates
		// Use a time before the test started to ensure updates will be detected
		initialTime := time.Now().Add(-10 * lastSeenUpdateThreshold)
		initialTimes := make(map[uid.ID]time.Time)
		for _, user := range users {
			initialTimes[user.ID] = initialTime
		}

		// Concurrently update all users
		numGoroutines := 10
		var wg sync.WaitGroup
		var errors []error
		var mu sync.Mutex

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(userIndex int) {
				defer wg.Done()
				user := users[userIndex%len(users)]
				// Create a copy with old LastSeenAt to force update
				userCopy := &models.Identity{
					Model:              user.Model,
					OrganizationMember: user.OrganizationMember,
					Name:               user.Name,
					LastSeenAt:         initialTime,
				}
				err := UpdateIdentityLastSeenAt(db, userCopy)
				mu.Lock()
				if err != nil {
					errors = append(errors, err)
				}
				mu.Unlock()
			}(i)
		}

		wg.Wait()

		// Check for errors
		if len(errors) > 0 {
			t.Errorf("Expected no errors, got %d: %v", len(errors), errors)
		}

		// Verify that all users were updated
		for _, user := range users {
			updatedUser, err := GetIdentity(db, GetIdentityOptions{ByID: user.ID})
			require.NoError(t, err)
			assert.True(t, updatedUser.LastSeenAt.After(initialTimes[user.ID]),
				"user %s LastSeenAt %v should be after initial time %v",
				user.Name, updatedUser.LastSeenAt, initialTimes[user.ID])
		}
	})
}
