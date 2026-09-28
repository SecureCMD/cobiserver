package web

import (
	"net/http"
	"strings"

	"cobiserver/internal/store"
)

func (s *Server) handleForgotForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "forgot_password.html", http.StatusOK, pageData{})
}

// handleForgot always shows the same generic confirmation message,
// regardless of whether the username exists, to avoid leaking account
// existence. The actual reset link is only ever sent by email (if SMTP is
// configured) or logged server-side for the administrator to relay.
func (s *Server) handleForgot(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "forgot_password.html", http.StatusBadRequest, pageData{"Error": "Invalid form submission."})
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))

	if s.LoginLimiter != nil && !s.LoginLimiter.Allow(s.clientIP(r)+"|forgot") {
		s.render(w, "forgot_password.html", http.StatusTooManyRequests, pageData{"Error": "Too many attempts. Try again in a minute."})
		return
	}

	const generic = "If that account exists, a reset link has been sent to its recovery email. If no email is on file, ask your server administrator to run: koserver gen-reset-link -username <name>"

	u, err := s.Store.GetUserByUsername(username)
	if err != nil {
		if err != store.ErrNotFound {
			s.Logger.Error("lookup user for reset failed", "err", err)
		}
		s.render(w, "forgot_password.html", http.StatusOK, pageData{"Info": generic})
		return
	}

	token, err := s.Store.CreatePasswordResetToken(u.ID, s.ResetTTL)
	if err != nil {
		s.Logger.Error("create reset token failed", "err", err)
		s.render(w, "forgot_password.html", http.StatusOK, pageData{"Info": generic})
		return
	}
	resetURL := s.BaseURL + "/account/reset-password?token=" + token

	if s.Mailer.Enabled() && u.Email != "" {
		if err := s.Mailer.SendPasswordReset(u.Email, resetURL); err != nil {
			s.Logger.Error("send reset email failed", "err", err)
		}
	} else {
		s.Logger.Info("password reset requested", "username", u.Username, "reset_url", resetURL)
	}

	s.render(w, "forgot_password.html", http.StatusOK, pageData{"Info": generic})
}

func (s *Server) handleResetForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		s.render(w, "forgot_password.html", http.StatusBadRequest, pageData{"Error": "Missing reset token."})
		return
	}
	s.render(w, "reset_password.html", http.StatusOK, pageData{"Token": token})
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "reset_password.html", http.StatusBadRequest, pageData{"Error": "Invalid form submission."})
		return
	}
	token := r.FormValue("token")
	newPass := r.FormValue("new_password")
	newPass2 := r.FormValue("new_password2")

	if newPass != newPass2 {
		s.render(w, "reset_password.html", http.StatusBadRequest, pageData{"Token": token, "Error": "Passwords do not match."})
		return
	}
	if len(newPass) < 4 {
		s.render(w, "reset_password.html", http.StatusBadRequest, pageData{"Token": token, "Error": "Password must be at least 4 characters."})
		return
	}

	userID, err := s.Store.ConsumePasswordResetToken(token)
	if err != nil {
		s.render(w, "reset_password.html", http.StatusBadRequest, pageData{"Error": "This reset link is invalid or has expired. Request a new one."})
		return
	}
	if err := s.Store.SetPasswordMD5(userID, store.MD5Hex(newPass)); err != nil {
		s.Logger.Error("reset set password failed", "err", err)
		s.render(w, "reset_password.html", http.StatusInternalServerError, pageData{"Error": "Something went wrong. Try again."})
		return
	}
	_ = s.Store.DeleteSessionsForUser(userID)
	http.Redirect(w, r, "/account/login?reset=1", http.StatusSeeOther)
}
