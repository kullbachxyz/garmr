package web

import (
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type loginView struct {
	CurrentUser *userView
	Error       string
	Next        string
}

type accountDetailsView struct {
	CurrentUser *userView
	Theme       string
	Error       string
	Success     string
}

type accountPasswordView struct {
	CurrentUser *userView
	Error       string
	Success     string
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	user, req := s.ensureUserFromCookie(w, r)
	if req != nil {
		r = req
	}
	next := sanitizeRedirect(r.URL.Query().Get("next"))
	if user != nil {
		http.Redirect(w, r, nextTarget(next), http.StatusSeeOther)
		return
	}

	switch r.Method {
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")
		next = sanitizeRedirect(r.FormValue("next"))
		if username == "" || password == "" {
			s.renderLogin(w, r, next, "Username and password are required")
			return
		}
		user, err := s.store.GetUserByUsername(username)
		if err != nil {
			s.renderLogin(w, r, next, "Invalid username or password")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
			s.renderLogin(w, r, next, "Invalid username or password")
			return
		}
		sessionID, err := s.store.CreateSession(user.ID)
		if err != nil {
			log.Printf("login: create session: %v", err)
			s.renderLogin(w, r, next, "Unable to create session. Check server logs.")
			return
		}
		s.store.UpdateLastLogin(user.ID)
		s.setSessionCookie(w, r, sessionID)
		http.Redirect(w, r, nextTarget(next), http.StatusSeeOther)
	default:
		s.renderLogin(w, r, next, "")
	}
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, next, message string) {
	data := loginView{
		CurrentUser: s.currentUser(r),
		Error:       message,
		Next:        next,
	}
	if err := s.tplLogin.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if cookie, err := r.Cookie(s.cookie); err == nil && cookie.Value != "" {
		_ = s.store.DeleteSession(cookie.Value)
	}
	s.clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleAccountDetails(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	theme := user.Theme
	if theme == "" {
		theme = "system"
	}
	data := accountDetailsView{CurrentUser: user, Theme: theme}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		intent := r.FormValue("intent")
		switch intent {
		case "theme":
			theme := strings.ToLower(strings.TrimSpace(r.FormValue("theme")))
			if theme == "" {
				theme = "system"
			}
			if err := s.store.UpdateTheme(user.ID, theme); err != nil {
				data.Error = err.Error()
			} else {
				user.Theme = theme
				data.Theme = theme
				data.Success = "Theme preference saved"
			}
		default:
			newUsername := strings.TrimSpace(r.FormValue("new_username"))
			current := r.FormValue("current_password")
			if newUsername == "" {
				data.Error = "Username is required"
			} else if current == "" {
				data.Error = "Current password is required"
			} else {
				stored, err := s.store.GetUserByID(user.ID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(current)) != nil {
					data.Error = "Current password is incorrect"
				} else if newUsername == stored.Username {
					data.Error = "Pick a different username"
				} else if err := s.store.UpdateUsername(user.ID, newUsername); err != nil {
					data.Error = err.Error()
				} else {
					user.Username = newUsername
					data.Success = "Username updated"
				}
			}
		}
	}
	if err := s.tplAccountDetails.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	data := accountPasswordView{CurrentUser: user}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		current := r.FormValue("current_password")
		newPass := r.FormValue("new_password")
		confirm := r.FormValue("confirm_password")

		if newPass != confirm {
			data.Error = "New passwords do not match"
		} else if len(newPass) < 8 {
			data.Error = "New password must be at least 8 characters"
		} else {
			stored, err := s.store.GetUserByID(user.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(current)) != nil {
				data.Error = "Current password is incorrect"
			} else if err := s.store.UpdatePassword(user.ID, newPass); err != nil {
				data.Error = err.Error()
			} else {
				newSession, err := s.store.CreateSession(user.ID)
				if err == nil {
					_ = s.store.DeleteSessionsForUserExcept(user.ID, newSession)
					s.setSessionCookie(w, r, newSession)
				}
				data.Success = "Password updated"
			}
		}
	}

	if err := s.tplAccountPass.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func sanitizeRedirect(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func nextTarget(next string) string {
	if next == "" {
		return "/"
	}
	return next
}
