package web

import (
	"context"
	"net/http"

	"cobiserver/internal/store"
)

type ctxKey int

const userCtxKey ctxKey = iota

func (s *Server) currentUser(r *http.Request) *store.User {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	sess, err := s.Store.GetSession(cookie.Value)
	if err != nil {
		return nil
	}
	u, err := s.Store.GetUserByID(sess.UserID)
	if err != nil {
		return nil
	}
	return u
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.SessionTTL.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			http.Redirect(w, r, "/account/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireLogin(func(w http.ResponseWriter, r *http.Request) {
		u := r.Context().Value(userCtxKey).(*store.User)
		if !u.IsAdmin {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

func userFromCtx(r *http.Request) *store.User {
	u, _ := r.Context().Value(userCtxKey).(*store.User)
	return u
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxyHeaders {
		if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
			return ip
		}
	}
	return r.RemoteAddr
}
