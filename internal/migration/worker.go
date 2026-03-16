package migration

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerPool manages concurrent task execution with a fixed number of workers.
type WorkerPool struct {
	workers          int
	logger           *slog.Logger
	consecutiveFails atomic.Int32
	maxConsecFails   int // consecutive failures before cooldown (default: 5)
}

// NewWorkerPool creates a pool with the given concurrency level.
func NewWorkerPool(workers int, logger *slog.Logger) *WorkerPool {
	if workers < 1 {
		workers = 1
	}
	return &WorkerPool{workers: workers, logger: logger, maxConsecFails: 5}
}

// TaskFunc is a function that processes a single task. Returns an error if it fails.
type TaskFunc func(ctx context.Context, task TableTask) error

// Run executes all tasks using the worker pool. Individual task failures are
// logged and recorded but do NOT cancel other workers — the pool continues
// processing remaining tasks. Only an explicit context cancellation (e.g. user
// cancel) stops all workers. Returns the first error encountered, or nil.
func (wp *WorkerPool) Run(ctx context.Context, tasks []TableTask, fn TaskFunc) error {
	taskCh := make(chan TableTask, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var (
		wg      sync.WaitGroup
		errOnce sync.Once
		firstErr error
	)

	for i := 0; i < wp.workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskCh {
				select {
				case <-ctx.Done():
					wp.logger.Warn("worker stopping due to cancellation",
						"worker", workerID, "reason", ctx.Err())
					return
				default:
				}

				// Circuit breaker: if too many consecutive failures, cool down
				// before picking up new work. This prevents cascading failures
				// when PG is temporarily unreachable.
				if fails := wp.consecutiveFails.Load(); fails >= int32(wp.maxConsecFails) {
					wp.logger.Warn("circuit breaker: too many consecutive failures, cooling down",
						"worker", workerID,
						"fails", fails,
						"cooldown", "30s")
					select {
					case <-time.After(30 * time.Second):
					case <-ctx.Done():
						return
					}
				}

				partName := jobPartitionKey(task)

				wp.logger.Info("worker starting task",
					"worker", workerID,
					"table", task.Table.Name,
					"partition", partName)

				if err := fn(ctx, task); err != nil {
					wp.consecutiveFails.Add(1)

					// Check if this is a context cancellation (user cancel)
					if ctx.Err() != nil {
						wp.logger.Warn("worker stopping due to cancellation",
							"worker", workerID, "reason", ctx.Err())
						return
					}

					wp.logger.Error("task failed, continuing with remaining tasks",
						"worker", workerID,
						"table", task.Table.Name,
						"partition", partName,
						"error", err)

					errOnce.Do(func() {
						firstErr = err
					})
					continue // pick up next task instead of stopping
				}

				wp.consecutiveFails.Store(0) // reset on success
				wp.logger.Info("worker completed task",
					"worker", workerID,
					"table", task.Table.Name,
					"partition", partName)
			}
		}(i)
	}

	wg.Wait()
	return firstErr
}
