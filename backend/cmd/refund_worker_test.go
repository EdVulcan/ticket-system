package main

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestRefundWorkerWakesImmediatelyAndKeepsPeriodicRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := make(chan time.Time, 4)
		wake := make(chan struct{}, 1)
		go runDigitalRefundLoop(ctx, wake, func(now time.Time) { calls <- now })
		synctest.Wait()
		start := <-calls
		// No elapsed time: a committed callback must bypass the periodic timer.
		wake <- struct{}{}
		synctest.Wait()
		select {
		case next := <-calls:
			if !next.Equal(start) {
				t.Fatalf("callback waited for a timer: %v", next.Sub(start))
			}
		default:
			t.Fatal("callback-due task still waiting for a timer")
		}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		select {
		case <-calls:
		default:
			t.Fatal("periodic recovery stopped")
		}
		cancel()
		synctest.Wait()
		time.Sleep(30 * time.Second)
		select {
		case <-calls:
			t.Fatal("worker continued after shutdown")
		default:
		}
	})
}
