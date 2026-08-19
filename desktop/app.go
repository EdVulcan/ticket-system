package main

import (
	"context"
	"encoding/json"
)

type App struct {
	ctx context.Context
}

type PrintTicketRequest struct {
	Document           json.RawMessage `json:"document"`
	ContentHash        string          `json:"content_hash"`
	TemplateRevisionID uint            `json:"template_revision_id"`
	PaperWidthMM       int             `json:"paper_width_mm"`
	Orientation        string          `json:"orientation"`
	CopyCount          int             `json:"copy_count"`
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
