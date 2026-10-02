package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"ticket-backend/internal/config"
	"ticket-backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

type temporaryQRTicket struct {
	Code string `json:"code"`
	Sold bool   `json:"sold"`
}
type temporaryQRState struct {
	Title    string              `json:"title"`
	Tickets  []temporaryQRTicket `json:"tickets"`
	Revision uint64              `json:"revision"`
}
type temporaryQRStore struct {
	mu sync.Mutex
}

func registerTemporaryQRAPI(engine *gin.Engine) {
	if _, err := os.Stat(filepath.Join(config.GlobalConfig.Server.AdminStaticDir, "temporary-qr", "index.html")); err != nil {
		return
	}
	store := &temporaryQRStore{}
	group := engine.Group("/api/temporary-qr")
	group.Use(middleware.RequestBodyLimit(2<<20), func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	group.GET("/state", store.getState)
	group.PUT("/state", store.putState)
}

func temporaryQRPath() string {
	if path := os.Getenv("TICKET_SERVER_TEMPORARY_QR_STORE_PATH"); path != "" {
		return path
	}
	return "data/temporary-qr.json"
}

func readTemporaryQRState() (temporaryQRState, error) {
	state := temporaryQRState{}
	bytes, err := os.ReadFile(temporaryQRPath())
	if os.IsNotExist(err) {
		seed, err := os.ReadFile(filepath.Join(config.GlobalConfig.Server.AdminStaticDir, "temporary-qr", "tickets.json"))
		if err != nil {
			return state, err
		}
		var codes []string
		if err = json.Unmarshal(seed, &codes); err != nil {
			return state, err
		}
		state.Title = "临时门票"
		state.Tickets = make([]temporaryQRTicket, 0, len(codes))
		for _, code := range codes {
			state.Tickets = append(state.Tickets, temporaryQRTicket{Code: code})
		}
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(bytes, &state)
	return state, err
}

func writeTemporaryQRState(state temporaryQRState) error {
	path := temporaryQRPath()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	bytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err = os.WriteFile(temp, bytes, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (s *temporaryQRStore) getState(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := readTemporaryQRState()
	if err != nil {
		c.JSON(500, gin.H{"error": "state unavailable"})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (s *temporaryQRStore) putState(c *gin.Context) {
	var state temporaryQRState
	if c.ShouldBindJSON(&state) != nil || len(state.Tickets) > 10000 || len(state.Title) > 320 {
		c.JSON(400, gin.H{"error": "invalid state"})
		return
	}
	seen := map[string]bool{}
	for _, ticket := range state.Tickets {
		if strings.TrimSpace(ticket.Code) == "" || len(ticket.Code) > 500 || seen[ticket.Code] {
			c.JSON(400, gin.H{"error": "invalid ticket"})
			return
		}
		seen[ticket.Code] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := readTemporaryQRState()
	if err != nil {
		c.JSON(500, gin.H{"error": "state unavailable"})
		return
	}
	if state.Revision != current.Revision {
		c.JSON(http.StatusConflict, gin.H{"error": "state changed"})
		return
	}
	state.Revision++
	if err := writeTemporaryQRState(state); err != nil {
		c.JSON(500, gin.H{"error": "state unavailable"})
		return
	}
	c.JSON(200, state)
}
