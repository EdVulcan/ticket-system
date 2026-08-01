package main

import "context"

type App struct {
	ctx context.Context
}

type PrintTicketRequest struct {
	OrderNo    string `json:"order_no"`
	TicketCode string `json:"ticket_code"`
}

type HardwareResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) PrintTicket(payload PrintTicketRequest) HardwareResult {
	_ = payload
	return HardwareResult{Success: false, Message: "printer adapter is not configured"}
}

func (a *App) ReadCard() HardwareResult {
	return HardwareResult{Success: false, Message: "identity card reader adapter is not configured"}
}
