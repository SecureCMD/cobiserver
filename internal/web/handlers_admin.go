package web

import (
	"net/http"
	"strings"

	"cobiserver/internal/store"
)

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	s.renderAdminUsers(w, http.StatusOK, "", "")
}

func (s *Server) renderAdminUsers(w http.ResponseWriter, status int, errMsg, info string) {
	users, err := s.Store.ListUsers()
	if err != nil {
		s.Logger.Error("list users failed", "err", err)
		errMsg = "Failed to load users."
	}
	data := pageData{"Users": users}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	if info != "" {
		data["Info"] = info
	}
	s.render(w, "admin_users.html", status, data)
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderAdminUsers(w, http.StatusBadRequest, "Invalid form submission.", "")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	isAdmin := r.FormValue("is_admin") == "1"

	if !store.ValidUsername(username) {
		s.renderAdminUsers(w, http.StatusBadRequest, "Invalid username.", "")
		return
	}
	if len(password) < 4 {
		s.renderAdminUsers(w, http.StatusBadRequest, "Password must be at least 4 characters.", "")
		return
	}
	u, err := s.Store.CreateUser(username, store.MD5Hex(password), email)
	if err == store.ErrExists {
		s.renderAdminUsers(w, http.StatusConflict, "That username already exists.", "")
		return
	}
	if err != nil {
		s.Logger.Error("admin create user failed", "err", err)
		s.renderAdminUsers(w, http.StatusInternalServerError, "Something went wrong.", "")
		return
	}
	if isAdmin {
		_ = s.Store.SetAdmin(u.Username, true)
	}
	s.renderAdminUsers(w, http.StatusOK, "", "User "+username+" created.")
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if err := r.ParseForm(); err != nil {
		s.renderAdminUsers(w, http.StatusBadRequest, "Invalid form submission.", "")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	if username == u.Username {
		s.renderAdminUsers(w, http.StatusBadRequest, "You cannot delete your own account.", "")
		return
	}
	if err := s.Store.DeleteUser(username); err != nil && err != store.ErrNotFound {
		s.Logger.Error("admin delete user failed", "err", err)
		s.renderAdminUsers(w, http.StatusInternalServerError, "Something went wrong.", "")
		return
	}
	s.renderAdminUsers(w, http.StatusOK, "", "User "+username+" deleted.")
}

// handleAdminResetPassword issues a password-reset link for a user without
// needing that user's current password or SMTP to be configured, so the
// administrator always has a way to help someone back into their account.
func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderAdminUsers(w, http.StatusBadRequest, "Invalid form submission.", "")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	target, err := s.Store.GetUserByUsername(username)
	if err != nil {
		s.renderAdminUsers(w, http.StatusNotFound, "User not found.", "")
		return
	}
	token, err := s.Store.CreatePasswordResetToken(target.ID, s.ResetTTL)
	if err != nil {
		s.Logger.Error("admin create reset token failed", "err", err)
		s.renderAdminUsers(w, http.StatusInternalServerError, "Something went wrong.", "")
		return
	}
	resetURL := s.BaseURL + "/account/reset-password?token=" + token
	s.renderAdminUsers(w, http.StatusOK, "", "Reset link for "+username+" (valid 1 hour): "+resetURL)
}
