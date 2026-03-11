package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	headerAuthToken = "X-Auth-Token"
	sessionDuration = 24 * time.Hour
	tokenLength     = 32
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type session struct {
	token     string
	expiresAt time.Time
}

type authManager struct {
	mu       sync.RWMutex
	sessions map[string]*session
	username string
	password string
	log      *slog.Logger
}

func newAuthManager(username, password string, log *slog.Logger) *authManager {
	am := &authManager{
		sessions: make(map[string]*session),
		username: username,
		password: password,
		log:      log,
	}

	// Start cleanup goroutine
	go am.cleanupExpiredSessions()

	return am
}

func (am *authManager) generateToken() (string, error) {
	b := make([]byte, tokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func (am *authManager) login(username, password string) (string, time.Time, error) {
	if am.username == "" || am.password == "" {
		// No auth configured, allow access
		return "", time.Time{}, nil
	}

	if username != am.username || password != am.password {
		return "", time.Time{}, fmt.Errorf("invalid credentials")
	}

	token, err := am.generateToken()
	if err != nil {
		return "", time.Time{}, err
	}

	expiresAt := time.Now().Add(sessionDuration)

	am.mu.Lock()
	am.sessions[token] = &session{
		token:     token,
		expiresAt: expiresAt,
	}
	am.mu.Unlock()

	return token, expiresAt, nil
}

func (am *authManager) validateToken(token string) bool {
	if am.username == "" || am.password == "" {
		// No auth configured, allow access
		return true
	}

	if token == "" {
		return false
	}

	am.mu.RLock()
	sess, ok := am.sessions[token]
	am.mu.RUnlock()

	if !ok {
		return false
	}

	if time.Now().After(sess.expiresAt) {
		am.mu.Lock()
		delete(am.sessions, token)
		am.mu.Unlock()
		return false
	}

	return true
}

func (am *authManager) cleanupExpiredSessions() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		am.mu.Lock()
		for token, sess := range am.sessions {
			if now.After(sess.expiresAt) {
				delete(am.sessions, token)
			}
		}
		am.mu.Unlock()
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	token, expiresAt, err := s.auth.login(req.Username, req.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	// If no auth is configured, return success without token
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(LoginResponse{
			Token:     "",
			ExpiresAt: time.Time{},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
	})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get(headerAuthToken)
		if !s.auth.validateToken(token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireAuthWS(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = r.Header.Get(headerAuthToken)
		}
		if !s.auth.validateToken(token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireAuthForWrites(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		token := r.Header.Get(headerAuthToken)
		if !s.auth.validateToken(token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
