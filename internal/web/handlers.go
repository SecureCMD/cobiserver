package web

import (
	"net/http"
	"strings"

	"cobiserver/internal/store"
)

type pageData map[string]any

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.currentUser(r) != nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/account/login", http.StatusSeeOther)
}

// ---- login / logout ----

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.currentUser(r) != nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	data := pageData{"AllowSignup": s.AllowSignup}
	if r.URL.Query().Get("reset") == "1" {
		data["Info"] = "Password reset. Sign in with your new password."
	}
	s.render(w, "login.html", http.StatusOK, data)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "login.html", http.StatusBadRequest, pageData{"Error": "Invalid form submission.", "AllowSignup": s.AllowSignup})
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if s.LoginLimiter != nil && !s.LoginLimiter.Allow(s.clientIP(r)+"|"+username) {
		s.render(w, "login.html", http.StatusTooManyRequests, pageData{"Error": "Too many attempts. Try again in a minute.", "AllowSignup": s.AllowSignup})
		return
	}

	u, ok, err := s.Store.VerifyMD5(username, store.MD5Hex(password))
	if err != nil {
		s.Logger.Error("login lookup failed", "err", err)
		s.render(w, "login.html", http.StatusInternalServerError, pageData{"Error": "Something went wrong. Try again.", "AllowSignup": s.AllowSignup})
		return
	}
	if !ok {
		s.render(w, "login.html", http.StatusUnauthorized, pageData{"Error": "Invalid username or password.", "AllowSignup": s.AllowSignup})
		return
	}
	sessID, err := s.Store.CreateSession(u.ID, s.SessionTTL)
	if err != nil {
		s.Logger.Error("create session failed", "err", err)
		s.render(w, "login.html", http.StatusInternalServerError, pageData{"Error": "Something went wrong. Try again.", "AllowSignup": s.AllowSignup})
		return
	}
	s.setSessionCookie(w, sessID)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.Store.DeleteSession(cookie.Value)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/account/login", http.StatusSeeOther)
}

// ---- registration ----

func (s *Server) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	if !s.AllowSignup {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	s.render(w, "register.html", http.StatusOK, pageData{})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.AllowSignup {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "register.html", http.StatusBadRequest, pageData{"Error": "Invalid form submission."})
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	password2 := r.FormValue("password2")

	if !store.ValidUsername(username) {
		s.render(w, "register.html", http.StatusBadRequest, pageData{"Error": "Username must be 3-64 characters: letters, digits, dot, dash, underscore."})
		return
	}
	if len(password) < 4 {
		s.render(w, "register.html", http.StatusBadRequest, pageData{"Error": "Password must be at least 4 characters."})
		return
	}
	if password != password2 {
		s.render(w, "register.html", http.StatusBadRequest, pageData{"Error": "Passwords do not match."})
		return
	}

	u, err := s.Store.CreateUser(username, store.MD5Hex(password), email)
	if err == store.ErrExists {
		s.render(w, "register.html", http.StatusConflict, pageData{"Error": "That username is already taken."})
		return
	}
	if err != nil {
		s.Logger.Error("register failed", "err", err)
		s.render(w, "register.html", http.StatusInternalServerError, pageData{"Error": "Something went wrong. Try again."})
		return
	}

	sessID, err := s.Store.CreateSession(u.ID, s.SessionTTL)
	if err != nil {
		s.Logger.Error("create session failed", "err", err)
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	s.setSessionCookie(w, sessID)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// ---- dashboard / profile ----

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	progress, err := s.Store.ListProgressForUser(u.ID)
	if err != nil {
		s.Logger.Error("list progress failed", "err", err)
	}
	s.render(w, "dashboard.html", http.StatusOK, pageData{
		"User":     u,
		"Progress": progress,
		"BaseURL":  s.BaseURL,
	})
}

func (s *Server) handleChangePasswordForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "change_password.html", http.StatusOK, pageData{})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if err := r.ParseForm(); err != nil {
		s.render(w, "change_password.html", http.StatusBadRequest, pageData{"Error": "Invalid form submission."})
		return
	}
	current := r.FormValue("current_password")
	newPass := r.FormValue("new_password")
	newPass2 := r.FormValue("new_password2")

	_, ok, err := s.Store.VerifyMD5(u.Username, store.MD5Hex(current))
	if err != nil {
		s.Logger.Error("verify current password failed", "err", err)
		s.render(w, "change_password.html", http.StatusInternalServerError, pageData{"Error": "Something went wrong. Try again."})
		return
	}
	if !ok {
		s.render(w, "change_password.html", http.StatusUnauthorized, pageData{"Error": "Current password is incorrect."})
		return
	}
	if len(newPass) < 4 {
		s.render(w, "change_password.html", http.StatusBadRequest, pageData{"Error": "New password must be at least 4 characters."})
		return
	}
	if newPass != newPass2 {
		s.render(w, "change_password.html", http.StatusBadRequest, pageData{"Error": "New passwords do not match."})
		return
	}
	if err := s.Store.SetPasswordMD5(u.ID, store.MD5Hex(newPass)); err != nil {
		s.Logger.Error("set password failed", "err", err)
		s.render(w, "change_password.html", http.StatusInternalServerError, pageData{"Error": "Something went wrong. Try again."})
		return
	}
	// Invalidate every session (including this one) so a stolen cookie
	// doesn't survive a deliberate password change, then issue a fresh
	// one for the browser that just proved it knows the new password.
	_ = s.Store.DeleteSessionsForUser(u.ID)
	if sessID, err := s.Store.CreateSession(u.ID, s.SessionTTL); err == nil {
		s.setSessionCookie(w, sessID)
	}
	s.render(w, "change_password.html", http.StatusOK, pageData{"Info": "Password updated. Re-enter it in the KOReader app."})
}

func (s *Server) handleChangeEmailForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "change_email.html", http.StatusOK, pageData{"User": userFromCtx(r)})
}

func (s *Server) handleChangeEmail(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if err := r.ParseForm(); err != nil {
		s.render(w, "change_email.html", http.StatusBadRequest, pageData{"User": u, "Error": "Invalid form submission."})
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if err := s.Store.SetEmail(u.ID, email); err != nil {
		s.Logger.Error("set email failed", "err", err)
		s.render(w, "change_email.html", http.StatusInternalServerError, pageData{"User": u, "Error": "Something went wrong. Try again."})
		return
	}
	u.Email = email
	s.render(w, "change_email.html", http.StatusOK, pageData{"User": u, "Info": "Email saved."})
}
