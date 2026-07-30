package model

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

var (
	ErrWriteQueueFull = errors.New("database write queue is full")
	ErrWriteTimeout   = errors.New("database write transaction timed out")
	ErrWriterClosed   = errors.New("database writer is closed")
	writer            *writeCoordinator
)

type writeJob struct {
	apply  func(*gorm.DB) error
	result chan error
}

type writeCoordinator struct {
	db             *gorm.DB
	jobs           chan writeJob
	enqueueTimeout time.Duration
	writeTimeout   time.Duration
	completed      atomic.Uint64
	mu             sync.RWMutex
	closed         bool
	closeOnce      sync.Once
	done           chan struct{}
}

func InitWriter(db *gorm.DB, queueSize int, enqueueTimeout, writeTimeout time.Duration) {
	coordinator := &writeCoordinator{
		db: db, jobs: make(chan writeJob, queueSize),
		enqueueTimeout: enqueueTimeout, writeTimeout: writeTimeout,
		done: make(chan struct{}),
	}
	writer = coordinator
	go coordinator.run()
}

func Write(apply func(*gorm.DB) error) error {
	if writer == nil {
		return errors.New("database writer is not initialized")
	}
	writer.mu.RLock()
	if writer.closed {
		writer.mu.RUnlock()
		return ErrWriterClosed
	}
	job := writeJob{apply: apply, result: make(chan error, 1)}
	timer := time.NewTimer(writer.enqueueTimeout)
	select {
	case writer.jobs <- job:
		writer.mu.RUnlock()
	case <-timer.C:
		writer.mu.RUnlock()
		return ErrWriteQueueFull
	}
	defer timer.Stop()
	return <-job.result
}

// CloseWriter stops accepting writes and waits for all accepted jobs to finish.
func CloseWriter(ctx context.Context) error {
	if writer == nil {
		return nil
	}
	writer.closeOnce.Do(func() {
		writer.mu.Lock()
		writer.closed = true
		writer.mu.Unlock()
		go func() {
			writer.jobs <- writeJob{}
		}()
	})
	select {
	case <-writer.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func WriteQueueDepth() int {
	if writer == nil {
		return 0
	}
	return len(writer.jobs)
}

func (w *writeCoordinator) run() {
	for job := range w.jobs {
		if job.apply == nil {
			close(w.done)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), w.writeTimeout)
		err := w.db.WithContext(ctx).Transaction(job.apply)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = ErrWriteTimeout
		}
		cancel()
		w.completed.Add(1)
		job.result <- err
	}
}
