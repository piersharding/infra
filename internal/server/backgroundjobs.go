package server

import (
	"context"
	"fmt"
	"time"

	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/data"
)

// BackgroundJobFunc is the interface for running periodic background jobs, like
// garbage collection. Errors and panics from the job will be logged by
// jobWrapper, and will not stop the goroutine running the job.
// The job should exit when ctx is cancelled. Unlike most transactions, the
// transaction passed to this job will not have an OrganizationID.
type BackgroundJobFunc func(tx data.WriteTxn) error

func backgroundJob(ctx context.Context, db *data.DB, job BackgroundJobFunc, every time.Duration) func() error {
	return func() error {
		t := time.NewTicker(every)
		funcName := getFuncName(job)

		jobWithRecover := func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			defer func() {
				if err := recover(); err != nil {
					secureLogger := logging.SecureLogger(logging.L)
					secureLogger.SecureError(fmt.Sprintf("background job %s panic", funcName), nil)
				}
			}()

			tx, err := db.Begin(ctx, nil)
			if err != nil {
				return fmt.Errorf("failed to start transaction :%w", err)
			}
			if err := job(tx); err != nil {
				_ = tx.Rollback()
				return err
			}
			return tx.Commit()
		}

		for {
			select {
			case <-t.C:
				startAt := time.Now().UTC()
				secureLogger := logging.SecureLogger(logging.L)
				secureLogger.SecureDebug(fmt.Sprintf("background job %s starting", funcName))
				if err := jobWithRecover(); err != nil {
					secureLogger.SecureError(fmt.Sprintf("background job %s error", funcName), err)
				} else {
					secureLogger.SecureInfo(fmt.Sprintf("background job %s successful, elapsed: %s", funcName, time.Since(startAt)))
				}
			case <-ctx.Done():
				t.Stop()
				return nil // time to quit.
			}
		}
	}
}
