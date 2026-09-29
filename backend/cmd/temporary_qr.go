package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"ticket-backend/internal/config"
	"ticket-backend/internal/middleware"
	"time"
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
	mu       sync.Mutex
	sessions map[string]time.Time
}

func registerTemporaryQRAPI(engine *gin.Engine) {
	if _, err := os.Stat(filepath.Join(config.GlobalConfig.Server.AdminStaticDir, "temporary-qr", "index.html")); err != nil {
		return
	}
	store := &temporaryQRStore{sessions: map[string]time.Time{}}
	group := engine.Group("/api/temporary-qr")
	group.Use(middleware.RequestBodyLimit(2<<20), func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	group.POST("/login", middleware.MiniappLoginRateLimit(), store.login)
	group.GET("/state", store.authenticated, store.getState)
	group.PUT("/state", store.authenticated, store.putState)
}

func (s *temporaryQRStore) login(c *gin.Context) {
	var request struct {
		Password string `json:"password"`
	}
	password := os.Getenv("TICKET_SERVER_TEMPORARY_QR_PASSWORD")
	if password == "" {
		password = "cbw123456"
	}
	expected := sha256.Sum256([]byte(password))
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(401, gin.H{"error": "invalid password"})
		return
	}
	actual := sha256.Sum256([]byte(request.Password))
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
		c.JSON(401, gin.H{"error": "invalid password"})
		return
	}
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		c.JSON(500, gin.H{"error": "session unavailable"})
		return
	}
	token := hex.EncodeToString(bytes)
	s.mu.Lock()
	for key, expiry := range s.sessions {
		if !expiry.After(time.Now()) {
			delete(s.sessions, key)
		}
	}
	s.sessions[token] = time.Now().Add(12 * time.Hour)
	s.mu.Unlock()
	c.JSON(200, gin.H{"token": token})
}

func (s *temporaryQRStore) authenticated(c *gin.Context) {
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	s.mu.Lock()
	expiry := s.sessions[token]
	s.mu.Unlock()
	if !expiry.After(time.Now()) {
		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
		return
	}
	c.Next()
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
