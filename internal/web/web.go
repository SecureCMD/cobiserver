// Package web provides the self-service account UI: login, registration,
// change password, forgot/reset password, and a small admin panel. It is
// entirely separate from the KOReader sync protocol in internal/kosync,
// but shares the same user store so a password change here is immediately
// usable from the KOReader app.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"cobiserver/internal/mailer"
	"cobiserver/internal/ratelimit"
	"cobiserver/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

const sessionCookieName = "koserver_session"

type Server struct {
	Store             *store.Store
	Mailer            *mailer.Mailer
	Logger            *slog.Logger
	BaseURL           string
	AllowSignup       bool
	CookieSecure      bool
	SessionTTL        time.Duration
	ResetTTL          time.Duration
	LoginLimiter      *ratelimit.Limiter
	TrustProxyHeaders bool

	tmpl *template.Template
}

func New(deps Server) (*Server, error) {
	t, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	deps.tmpl = t
	return &deps, nil
}

func (s *Server) render(w http.ResponseWriter, name string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.Logger.Error("template render failed", "template", name, "err", err)
	}
}

func (s *Server) Routes(mux *http.ServeMux) {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /account/login", s.handleLoginForm)
	mux.HandleFunc("POST /account/login", s.handleLogin)
	mux.HandleFunc("POST /account/logout", s.handleLogout)
	mux.HandleFunc("GET /account/register", s.handleRegisterForm)
	mux.HandleFunc("POST /account/register", s.handleRegister)
	mux.HandleFunc("GET /account/forgot-password", s.handleForgotForm)
	mux.HandleFunc("POST /account/forgot-password", s.handleForgot)
	mux.HandleFunc("GET /account/reset-password", s.handleResetForm)
	mux.HandleFunc("POST /account/reset-password", s.handleReset)

	mux.HandleFunc("GET /account", s.requireLogin(s.handleDashboard))
	mux.HandleFunc("GET /account/change-password", s.requireLogin(s.handleChangePasswordForm))
	mux.HandleFunc("POST /account/change-password", s.requireLogin(s.handleChangePassword))
	mux.HandleFunc("GET /account/change-email", s.requireLogin(s.handleChangeEmailForm))
	mux.HandleFunc("POST /account/change-email", s.requireLogin(s.handleChangeEmail))

	mux.HandleFunc("GET /admin/users", s.requireAdmin(s.handleAdminUsers))
	mux.HandleFunc("POST /admin/users/create", s.requireAdmin(s.handleAdminCreateUser))
	mux.HandleFunc("POST /admin/users/delete", s.requireAdmin(s.handleAdminDeleteUser))
	mux.HandleFunc("POST /admin/users/reset-password", s.requireAdmin(s.handleAdminResetPassword))
}
