// Package kosync implements the wire-compatible KOReader sync server API:
// POST /users/create, GET /users/auth, PUT /syncs/progress and
// GET /syncs/progress/{document}, matching the JSON contract described in
// koreader's plugins/kosync.koplugin/api.json and consumed by
// KOSyncClient.lua / main.lua.
package kosync

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"cobiserver/internal/ratelimit"
	"cobiserver/internal/store"
)

type Server struct {
	Store             *store.Store
	AllowSignup       bool
	Logger            *slog.Logger
	AuthLimiter       *ratelimit.Limiter
	TrustProxyHeaders bool
}

type ctxKey int

const userCtxKey ctxKey = iota

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxyHeaders {
		if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
			return ip
		}
	}
	host, _, err := splitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func splitHostPort(addr string) (string, string, error) {
	// net.SplitHostPort without importing net just for this in the hot path.
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:], nil
		}
	}
	return addr, "", nil
}

// requireAuth enforces the X-Auth-User / X-Auth-Key headers used by every
// KOReader sync endpoint except registration.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("x-auth-user")
		key := r.Header.Get("x-auth-key")
		if username == "" || key == "" {
			writeMessage(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if s.AuthLimiter != nil && !s.AuthLimiter.Allow(s.clientIP(r)+"|"+username) {
			writeMessage(w, http.StatusTooManyRequests, "Too many attempts, slow down.")
			return
		}
		u, ok, err := s.Store.VerifyMD5(username, key)
		if err != nil {
			s.Logger.Error("auth lookup failed", "err", err)
			writeMessage(w, http.StatusInternalServerError, "Unknown server error")
			return
		}
		if !ok {
			writeMessage(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		next(w, r.WithContext(ctx))
	}
}

func userFromCtx(r *http.Request) *store.User {
	u, _ := r.Context().Value(userCtxKey).(*store.User)
	return u
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// HandleCreateUser implements POST /users/create.
// Expected statuses per the client: 201 (created) or 402 (already exists).
func (s *Server) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.AllowSignup {
		writeMessage(w, http.StatusForbidden, "Registration is disabled on this server.")
		return
	}
	var req createUserRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if req.Username == "" || req.Password == "" || !store.ValidUsername(req.Username) {
		writeMessage(w, http.StatusBadRequest, "Invalid username or password.")
		return
	}
	if s.AuthLimiter != nil && !s.AuthLimiter.Allow(s.clientIP(r)+"|register") {
		writeMessage(w, http.StatusTooManyRequests, "Too many attempts, slow down.")
		return
	}
	_, err := s.Store.CreateUser(req.Username, req.Password, "")
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username})
	case err == store.ErrExists:
		writeMessage(w, http.StatusPaymentRequired, "Username is already registered.")
	case err == store.ErrInvalidInput:
		writeMessage(w, http.StatusBadRequest, "Invalid username or password.")
	default:
		s.Logger.Error("create user failed", "err", err)
		writeMessage(w, http.StatusInternalServerError, "Unknown server error")
	}
}

// HandleAuth implements GET /users/auth.
func (s *Server) HandleAuth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"authorized": "OK"})
}

type updateProgressRequest struct {
	Document   string  `json:"document"`
	Progress   any     `json:"progress"`
	Percentage float64 `json:"percentage"`
	Device     string  `json:"device"`
	DeviceID   string  `json:"device_id"`
	Metadata   any     `json:"metadata,omitempty"`
}

// HandleUpdateProgress implements PUT /syncs/progress.
func (s *Server) HandleUpdateProgress(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var req updateProgressRequest
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if req.Document == "" {
		writeMessage(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	progressStr := toJSONString(req.Progress)
	metaStr := toJSONString(req.Metadata)
	err := s.Store.UpsertProgress(u.ID, store.Progress{
		Document:    req.Document,
		Percentage:  req.Percentage,
		ProgressStr: progressStr,
		Device:      req.Device,
		DeviceID:    req.DeviceID,
		Metadata:    metaStr,
	})
	if err != nil {
		s.Logger.Error("upsert progress failed", "err", err)
		writeMessage(w, http.StatusInternalServerError, "Unknown server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"document":  req.Document,
		"timestamp": time.Now().Unix(),
	})
}

// HandleGetProgress implements GET /syncs/progress/{document}.
func (s *Server) HandleGetProgress(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	document := r.PathValue("document")
	if document == "" {
		writeMessage(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	p, err := s.Store.GetProgress(u.ID, document)
	if err == store.ErrNotFound {
		// No progress recorded yet: KOReader treats a response without a
		// "percentage" field as "nothing to sync" and does not error out.
		writeJSON(w, http.StatusOK, map[string]string{"document": document})
		return
	}
	if err != nil {
		s.Logger.Error("get progress failed", "err", err)
		writeMessage(w, http.StatusInternalServerError, "Unknown server error")
		return
	}
	resp := map[string]any{
		"document":   p.Document,
		"percentage": p.Percentage,
		"device":     p.Device,
		"device_id":  p.DeviceID,
		"timestamp":  p.UpdatedAt.Unix(),
	}
	if p.ProgressStr != "" {
		resp["progress"] = fromJSONString(p.ProgressStr)
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleHealthcheck is a plain liveness probe for Docker/Compose.
func (s *Server) HandleHealthcheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"state": "ok"})
}

func toJSONString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func fromJSONString(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /users/create", s.HandleCreateUser)
	mux.HandleFunc("GET /users/auth", s.requireAuth(s.HandleAuth))
	mux.HandleFunc("PUT /syncs/progress", s.requireAuth(s.HandleUpdateProgress))
	mux.HandleFunc("GET /syncs/progress/{document}", s.requireAuth(s.HandleGetProgress))
	mux.HandleFunc("GET /healthcheck", s.HandleHealthcheck)
}
