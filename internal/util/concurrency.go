package util

import (
	"context"
	"sync"
)

// ConcurrentTask represents a task that can be executed concurrently
type ConcurrentTask func() error

// RunConcurrent executes tasks with bounded concurrency (maxConcurrency goroutines at once)
// Returns the first error encountered, or nil if all tasks succeed
func RunConcurrent(tasks []ConcurrentTask, maxConcurrency int) error {
	if len(tasks) == 0 {
		return nil
	}

	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	// Semaphore channel to limit concurrency
	semaphore := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	errChan := make(chan error, len(tasks))

	// Context for cancellation if needed
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start all tasks
	for _, task := range tasks {
		wg.Add(1)
		go func(t ConcurrentTask) {
			defer wg.Done()

			// Acquire semaphore slot
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}

			// Release semaphore slot when done
			defer func() { <-semaphore }()

			// Execute task
			if err := t(); err != nil {
				select {
				case errChan <- err:
					cancel() // Cancel other tasks on first error
				default:
				}
			}
		}(task)
	}

	// Wait for all tasks to complete
	wg.Wait()
	close(errChan)

	// Return first error if any
	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}

// RunConcurrentWithContext same as RunConcurrent but with external context control
func RunConcurrentWithContext(ctx context.Context, tasks []ConcurrentTask, maxConcurrency int) error {
	if len(tasks) == 0 {
		return nil
	}

	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	semaphore := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	errChan := make(chan error, len(tasks))

	for _, task := range tasks {
		wg.Add(1)
		go func(t ConcurrentTask) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}

			defer func() { <-semaphore }()

			if err := t(); err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}(task)
	}

	wg.Wait()
	close(errChan)

	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}
