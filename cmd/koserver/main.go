// Command koserver runs a KOReader-sync-protocol-compatible server, and
// also doubles as its own admin CLI (create/list/delete users, reset
// passwords, promote admins) so a self-hoster never needs direct SQL
// access to recover an account.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cobiserver/internal/config"
	"cobiserver/internal/kosync"
	"cobiserver/internal/mailer"
	"cobiserver/internal/ratelimit"
	"cobiserver/internal/store"
	"cobiserver/internal/web"
)

// version, commit and date are set at build time via -ldflags, see the
// Makefile's LDFLAGS. They default to these placeholders for `go run` /
// unversioned builds.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		runServe()
		return
	}
	switch os.Args[1] {
	case "serve":
		runServe()
	case "create-user":
		runCreateUser()
	case "set-password":
		runSetPassword()
	case "list-users":
		runListUsers()
	case "delete-user":
		runDeleteUser()
	case "make-admin":
		runMakeAdmin(true)
	case "revoke-admin":
		runMakeAdmin(false)
	case "gen-reset-link":
		runGenResetLink()
	case "version", "-v", "--version":
		fmt.Printf("koserver %s (commit %s, built %s)\n", version, commit, date)
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "koserver: unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Println(`koserver: KOReader-compatible sync server

Usage:
  koserver [serve]                                   Run the HTTP server (default)
  koserver create-user -username U -password P [-email E] [-admin]
  koserver set-password -username U -password P
  koserver list-users
  koserver delete-user -username U
  koserver make-admin -username U
  koserver revoke-admin -username U
  koserver gen-reset-link -username U
  koserver version

Configuration is read from environment variables; see README.md.`)
}

func openStore() *store.Store {
	cfg := config.Load()
	s, err := store.Open(cfg.DBPath, cfg.DBMaxReadConns)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open store:", err)
		os.Exit(1)
	}
	return s
}

func runCreateUser() {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	username := fs.String("username", "", "username")
	password := fs.String("password", "", "password")
	email := fs.String("email", "", "recovery email (optional)")
	admin := fs.Bool("admin", false, "make this user an admin")
	_ = fs.Parse(os.Args[2:])
	if *username == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "usage: koserver create-user -username U -password P [-email E] [-admin]")
		os.Exit(2)
	}
	s := openStore()
	defer s.Close()
	u, err := s.CreateUser(*username, store.MD5Hex(*password), *email)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create user:", err)
		os.Exit(1)
	}
	if *admin {
		if err := s.SetAdmin(u.Username, true); err != nil {
			fmt.Fprintln(os.Stderr, "set admin:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("created user %q (admin=%v)\n", u.Username, *admin)
}

func runSetPassword() {
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	username := fs.String("username", "", "username")
	password := fs.String("password", "", "new password")
	_ = fs.Parse(os.Args[2:])
	if *username == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "usage: koserver set-password -username U -password P")
		os.Exit(2)
	}
	s := openStore()
	defer s.Close()
	u, err := s.GetUserByUsername(*username)
	if err != nil {
		fmt.Fprintln(os.Stderr, "user not found:", err)
		os.Exit(1)
	}
	if err := s.SetPasswordMD5(u.ID, store.MD5Hex(*password)); err != nil {
		fmt.Fprintln(os.Stderr, "set password:", err)
		os.Exit(1)
	}
	_ = s.DeleteSessionsForUser(u.ID)
	fmt.Printf("password updated for %q\n", u.Username)
}

func runListUsers() {
	s := openStore()
	defer s.Close()
	users, err := s.ListUsers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "list users:", err)
		os.Exit(1)
	}
	for _, u := range users {
		fmt.Printf("%-24s email=%-28s admin=%-5v created=%s\n", u.Username, orDash(u.Email), u.IsAdmin, u.CreatedAt.Format(time.RFC3339))
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func runDeleteUser() {
	fs := flag.NewFlagSet("delete-user", flag.ExitOnError)
	username := fs.String("username", "", "username")
	_ = fs.Parse(os.Args[2:])
	if *username == "" {
		fmt.Fprintln(os.Stderr, "usage: koserver delete-user -username U")
		os.Exit(2)
	}
	s := openStore()
	defer s.Close()
	if err := s.DeleteUser(*username); err != nil {
		fmt.Fprintln(os.Stderr, "delete user:", err)
		os.Exit(1)
	}
	fmt.Printf("deleted user %q\n", *username)
}

func runMakeAdmin(admin bool) {
	fs := flag.NewFlagSet("make-admin", flag.ExitOnError)
	username := fs.String("username", "", "username")
	_ = fs.Parse(os.Args[2:])
	if *username == "" {
		fmt.Fprintln(os.Stderr, "usage: koserver make-admin -username U")
		os.Exit(2)
	}
	s := openStore()
	defer s.Close()
	if err := s.SetAdmin(*username, admin); err != nil {
		fmt.Fprintln(os.Stderr, "set admin:", err)
		os.Exit(1)
	}
	fmt.Printf("%q admin=%v\n", *username, admin)
}

func runGenResetLink() {
	fs := flag.NewFlagSet("gen-reset-link", flag.ExitOnError)
	username := fs.String("username", "", "username")
	_ = fs.Parse(os.Args[2:])
	if *username == "" {
		fmt.Fprintln(os.Stderr, "usage: koserver gen-reset-link -username U")
		os.Exit(2)
	}
	cfg := config.Load()
	s := openStore()
	defer s.Close()
	u, err := s.GetUserByUsername(*username)
	if err != nil {
		fmt.Fprintln(os.Stderr, "user not found:", err)
		os.Exit(1)
	}
	token, err := s.CreatePasswordResetToken(u.ID, cfg.ResetTTL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create reset token:", err)
		os.Exit(1)
	}
	fmt.Printf("%s/account/reset-password?token=%s\n(valid for %s)\n", cfg.BaseURL, token, cfg.ResetTTL)
}

func runServe() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	s, err := store.Open(cfg.DBPath, cfg.DBMaxReadConns)
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer s.Close()

	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for range t.C {
			_ = s.CleanupExpiredSessions()
			_ = s.CleanupExpiredResetTokens()
		}
	}()

	m := mailer.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom, cfg.SMTPStartTLS)

	authLimiter := ratelimit.New(30, time.Minute)
	loginLimiter := ratelimit.New(10, time.Minute)

	kos := &kosync.Server{
		Store:             s,
		AllowSignup:       cfg.AllowSignup,
		Logger:            logger,
		AuthLimiter:       authLimiter,
		TrustProxyHeaders: cfg.TrustProxyHeaders,
	}

	webSrv, err := web.New(web.Server{
		Store:             s,
		Mailer:            m,
		Logger:            logger,
		BaseURL:           cfg.BaseURL,
		AllowSignup:       cfg.AllowSignup,
		CookieSecure:      cfg.CookieSecure,
		SessionTTL:        cfg.SessionTTL,
		ResetTTL:          cfg.ResetTTL,
		LoginLimiter:      loginLimiter,
		TrustProxyHeaders: cfg.TrustProxyHeaders,
	})
	if err != nil {
		logger.Error("init web server", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	kos.Routes(mux)
	webSrv.Routes(mux)

	handler := logMiddleware(logger, mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if !m.Enabled() {
		logger.Warn("SMTP not configured: forgot-password requests will be logged here instead of emailed; use 'koserver gen-reset-link -username <name>' to help users recover accounts")
	}
	if cfg.AllowSignup {
		logger.Info("self-registration is enabled (ALLOW_SIGNUP=true)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL, "db", cfg.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}

func logMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(lrw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggingResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
