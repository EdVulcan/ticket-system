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
	ErrWriteTimeout = errors.New("database write transaction timed out")
	ErrWriterClosed = errors.New("database writer is closed")
	writer          *writeCoordinator
)

type writeCoordinator struct {
	db           *gorm.DB
	writeTimeout time.Duration
	completed    atomic.Uint64
	mu           sync.RWMutex
	active       sync.WaitGroup
	closed       bool
	closeOnce    sync.Once
	done         chan struct{}
}

func InitWriter(db *gorm.DB, writeTimeout time.Duration) {
	writer = &writeCoordinator{db: db, writeTimeout: writeTimeout, done: make(chan struct{})}
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
	writer.active.Add(1)
	writer.mu.RUnlock()
	defer writer.active.Done()

	ctx, cancel := context.WithTimeout(context.Background(), writer.writeTimeout)
	defer cancel()
	err := writer.db.WithContext(ctx).Transaction(apply)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrWriteTimeout
	}
	writer.completed.Add(1)
	return err
}

// CloseWriter stops accepting writes and waits for active PostgreSQL
// transactions to finish.
func CloseWriter(ctx context.Context) error {
	if writer == nil {
		return nil
	}
	writer.closeOnce.Do(func() {
		writer.mu.Lock()
		writer.closed = true
		writer.mu.Unlock()
		go func() {
			writer.active.Wait()
			close(writer.done)
		}()
	})
	select {
	case <-writer.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
