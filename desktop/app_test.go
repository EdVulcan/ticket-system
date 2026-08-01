package main

import "testing"

func TestHardwareAdaptersFailClosedUntilConfigured(t *testing.T) {
	app := NewApp()
	printed := app.PrintTicket(PrintTicketRequest{OrderNo: "ORDER-1", TicketCode: "TICKET-1"})
	if printed.Success || printed.Message != "printer adapter is not configured" {
		t.Fatalf("unexpected print result: %+v", printed)
	}
	card := app.ReadCard()
	if card.Success || card.Message != "identity card reader adapter is not configured" {
		t.Fatalf("unexpected card result: %+v", card)
	}
}
