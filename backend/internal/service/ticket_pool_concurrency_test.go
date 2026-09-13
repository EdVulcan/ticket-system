package service

import (
	"sync"
	"sync/atomic"
	"testing"

	"ticket-backend/internal/model"
)

func TestPooledLastSelectionConcurrentAcrossCheckpoints(t *testing.T) {
	resetBusinessData(t)
	f := seedPooledTicketFixture(t, "order", 4, 3, 1)
	ticket := createPaidPooledOrder(t, f, 6)
	for point, count := range []int{6, 6, 5} {
		for i := 0; i < count; i++ {
			if err := verifyPooledTicket(t, f, ticket, point); err != nil {
				t.Fatal(err)
			}
		}
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(point int) {
			defer wg.Done()
			<-start
			if err := (&TicketService{}).Verify(ticket.TicketCode, f.checkpoints[point].ID, f.devices[point].ID, f.tenantID); err == nil {
				successes.Add(1)
			}
		}(2 + i%2)
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("last group slot consumed %d times", successes.Load())
	}
	var stored model.Ticket
	model.DB.First(&stored, ticket.ID)
	if stored.CheckInCount != 18 || stored.Status != "used" {
		t.Fatalf("status=%s count=%d", stored.Status, stored.CheckInCount)
	}
}

func TestPooledRepeatAllowanceRemainsUsableWhenSelectionsAreFull(t *testing.T) {
	resetBusinessData(t)
	f := seedPooledTicketFixture(t, "order", 4, 3, 3)
	ticket := createPaidPooledOrder(t, f, 1)
	for point := 0; point < 3; point++ {
		if err := verifyPooledTicket(t, f, ticket, point); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyPooledTicket(t, f, ticket, 3); err == nil {
		t.Fatal("fourth selection allowed")
	}
	for point := 0; point < 3; point++ {
		for i := 0; i < 2; i++ {
			if err := verifyPooledTicket(t, f, ticket, point); err != nil {
				t.Fatalf("existing selected point lost repeats: %v", err)
			}
		}
	}
	var stored model.Ticket
	model.DB.First(&stored, ticket.ID)
	if stored.CheckInCount != 9 || stored.Status != "used" {
		t.Fatalf("status=%s count=%d", stored.Status, stored.CheckInCount)
	}
}
