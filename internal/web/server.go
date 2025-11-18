package web

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"garmr/internal/cfg"
	"garmr/internal/importer"
	"garmr/internal/store"
)

//go:embed views/*.tmpl
var tplFS embed.FS

//go:embed static/*
var staticFS embed.FS

const sessionCookieName = "garmr_session"
const sessionDuration = 30 * 24 * time.Hour

type ctxKey string

var userCtxKey ctxKey = "user"

type userView struct {
	ID       int64
	Username string
	Theme    string
}

type Server struct {
	cfg    cfg.Config
	db     *sql.DB
	store  *store.DB
	mux    *http.ServeMux
	im     *importer.Importer
	cookie string

	// separate template sets (each has layout + that page's content)
	tplDash           *template.Template
	tplList           *template.Template
	tplDetail         *template.Template
	tplStats          *template.Template
	tplImport         *template.Template
	tplLogin          *template.Template
	tplAccountDetails *template.Template
	tplAccountPass    *template.Template
	tplCalendar       *template.Template
}

func New(c cfg.Config, db *store.DB, im *importer.Importer) *http.Server {
	mux := http.NewServeMux()
	s := &Server{
		cfg:    c,
		db:     db.DB,
		store:  db,
		mux:    mux,
		im:     im,
		cookie: sessionCookieName,
	}

	// helpers used by templates
	themeValue := func(u *userView) string {
		if u == nil || u.Theme == "" {
			return "system"
		}
		return u.Theme
	}

	toFloat := func(v any) float64 {
		switch x := v.(type) {
		case int:
			return float64(x)
		case int64:
			return float64(x)
		case float64:
			return x
		case float32:
			return float64(x)
		default:
			return 0
		}
	}

	// local tz: Europe/Berlin (fallback to system local)
	loc, _ := time.LoadLocation("Europe/Berlin")
	if loc == nil {
		loc = time.Local
	}

	funcMap := template.FuncMap{
		"div": func(a any, b any) float64 {
			bb := toFloat(b)
			if bb == 0 {
				return 0
			}
			return toFloat(a) / bb
		},
		"mul": func(a any, b any) float64 { return toFloat(a) * toFloat(b) },

		// Time formatting (if you ever pass time.Time to tmpl)
		"fmtTime": func(t time.Time) string { return t.In(loc).Format("2006-01-02 15:04") },

		// Strip noisy " UTC" variants from stored timestamp strings.
		"trimUTC": func(s string) string {
			s = strings.ReplaceAll(s, " +0000 UTC", "")
			s = strings.TrimSuffix(s, " UTC")
			return s
		},

		"fmtDuration": func(sec int) string {
			if sec < 60 {
				return fmt.Sprintf("%ds", sec)
			}
			m := sec / 60
			sr := sec % 60
			if m < 60 {
				if sr == 0 {
					return fmt.Sprintf("%d min", m)
				}
				return fmt.Sprintf("%d min %d s", m, sr)
			}
			h := m / 60
			m = m % 60
			if m == 0 {
				return fmt.Sprintf("%dh", h)
			}
			return fmt.Sprintf("%dh %dmin", h, m)
		},

		// Pace from average speed in m/s -> "m:ss /km"
		"fmtPace": func(avgSpeedMPS float64) string {
			if avgSpeedMPS <= 0 {
				return "-"
			}
			p := 1000.0 / avgSpeedMPS // seconds per km
			min := int(p) / 60
			sec := int(p) % 60
			return fmt.Sprintf("%d:%02d /km", min, sec)
		},

		"sportClass": func(s string) string {
			ls := strings.ToLower(strings.TrimSpace(s))
			switch {
			case strings.Contains(ls, "run"):
				return "sport-running"
			case strings.Contains(ls, "cycl"), strings.Contains(ls, "bik"):
				return "sport-cycling"
			case strings.Contains(ls, "walk"):
				return "sport-walking"
			case strings.Contains(ls, "hik"):
				return "sport-hiking"
			case strings.Contains(ls, "swim"):
				return "sport-swimming"
			default:
				return "sport-generic"
			}
		},

		"themeValue": themeValue,
	}

	// Parse base layout, then clone per page to avoid "content" conflicts
	base := template.Must(template.New("layout").Funcs(funcMap).ParseFS(tplFS, "views/layout.tmpl"))
	s.tplDash = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/dashboard.tmpl"))
	s.tplList = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/activities_list.tmpl"))
	s.tplDetail = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/activity_detail.tmpl"))
	s.tplStats = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/stats.tmpl"))
	s.tplImport = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/import.tmpl"))
	s.tplLogin = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/login.tmpl"))
	s.tplAccountDetails = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/account_details.tmpl"))
	s.tplAccountPass = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/account_password.tmpl"))
	s.tplCalendar = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/calendar.tmpl"))

	// routes
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/favicon.svg")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(data)
	})
	mux.Handle("/login", http.HandlerFunc(s.handleLogin))
	mux.Handle("/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("/account", s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/account/details", http.StatusSeeOther)
	})))
	mux.Handle("/account/details", s.requireAuth(http.HandlerFunc(s.handleAccountDetails)))
	mux.Handle("/account/password", s.requireAuth(http.HandlerFunc(s.handleAccountPassword)))

	mux.Handle("/", s.requireAuth(http.HandlerFunc(s.handleDashboard)))
	mux.Handle("/activities", s.requireAuth(http.HandlerFunc(s.handleActivities)))
	mux.Handle("/activity/delete", s.requireAuth(http.HandlerFunc(s.handleActivityDelete)))
	mux.Handle("/activity/download", s.requireAuth(http.HandlerFunc(s.handleActivityDownload)))
	mux.Handle("/activity/", s.requireAuth(http.HandlerFunc(s.handleActivityDetail)))
	mux.Handle("/api/activity/", s.requireAuth(http.HandlerFunc(s.handleActivityGeoJSON)))
	mux.Handle("/api/import", s.requireAuth(http.HandlerFunc(s.handleImportNow))) // POST
	mux.Handle("/api/logs", s.requireAuth(http.HandlerFunc(s.handleLogsSSE)))     // GET (SSE)
	mux.Handle("/api/series/", s.requireAuth(http.HandlerFunc(s.handleActivitySeries)))
	mux.Handle("/api/zones/", s.requireAuth(http.HandlerFunc(s.handleActivityZones)))
	mux.Handle("/stats", s.requireAuth(http.HandlerFunc(s.handleStatsPage)))
	mux.Handle("/api/stats", s.requireAuth(http.HandlerFunc(s.handleStatsData)))
	mux.Handle("/api/stats/periods", s.requireAuth(http.HandlerFunc(s.handleStatsPeriods)))
	mux.Handle("/calendar", s.requireAuth(http.HandlerFunc(s.handleCalendar)))
	mux.Handle("/import", s.requireAuth(http.HandlerFunc(s.handleImportPage)))
	mux.Handle("/api/upload", s.requireAuth(http.HandlerFunc(s.handleFileUpload))) // POST

	return &http.Server{Addr: c.HTTPAddr, Handler: s.withSession(mux)}
}

func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cookie, err := r.Cookie(s.cookie)
		if err == nil && cookie.Value != "" {
			session, serr := s.store.GetSession(cookie.Value)
			if serr == nil {
				user, uerr := s.store.GetUserByID(session.UserID)
				if uerr == nil {
					ctx = context.WithValue(ctx, userCtxKey, &userView{ID: user.ID, Username: user.Username, Theme: user.Theme})
				} else {
					_ = s.store.DeleteSession(cookie.Value)
					log.Printf("auth: clearing cookie, user lookup failed: %v", uerr)
					s.clearSessionCookie(w, r)
				}
			} else {
				if errors.Is(serr, sql.ErrNoRows) {
					log.Printf("auth: clearing cookie, session missing")
					s.clearSessionCookie(w, r)
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, req := s.ensureUserFromCookie(w, r)
		if user == nil {
			target := r.URL.RequestURI()
			if target == "" {
				target = "/"
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(target), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func (s *Server) currentUser(r *http.Request) *userView {
	if val, ok := r.Context().Value(userCtxKey).(*userView); ok {
		return val
	}
	return nil
}

func (s *Server) ensureUserFromCookie(w http.ResponseWriter, r *http.Request) (*userView, *http.Request) {
	if user := s.currentUser(r); user != nil {
		return user, r
	}
	cookie, err := r.Cookie(s.cookie)
	if err != nil || cookie.Value == "" {
		return nil, r
	}
	session, err := s.store.GetSession(cookie.Value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("auth: no session for cookie %s", cookie.Value)
			s.clearSessionCookie(w, r)
		}
		return nil, r
	}
	user, err := s.store.GetUserByID(session.UserID)
	if err != nil {
		_ = s.store.DeleteSession(cookie.Value)
		log.Printf("auth: user lookup failed for session %s: %v", cookie.Value, err)
		s.clearSessionCookie(w, r)
		return nil, r
	}
	uv := &userView{ID: user.ID, Username: user.Username, Theme: user.Theme}
	ctx := context.WithValue(r.Context(), userCtxKey, uv)
	return uv, r.WithContext(ctx)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, value string) {
	c := &http.Cookie{
		Name:     s.cookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(sessionDuration.Seconds()),
		SameSite: http.SameSiteLaxMode,
	}
	if r != nil && r.TLS != nil {
		c.Secure = true
	}
	http.SetCookie(w, c)
	log.Printf("auth: set cookie for session %s", value)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	c := &http.Cookie{
		Name:     s.cookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if r != nil && r.TLS != nil {
		c.Secure = true
	}
	http.SetCookie(w, c)
	log.Printf("auth: cleared session cookie")
}
