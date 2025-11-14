package web

import (
	"crypto/subtle"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"net/http"
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

type Server struct {
	cfg cfg.Config
	db  *sql.DB
	mux *http.ServeMux
	im  *importer.Importer

	// separate template sets (each has layout + that page's content)
	tplDash   *template.Template
	tplList   *template.Template
	tplDetail *template.Template
	tplStats  *template.Template
	tplImport *template.Template
}

func New(c cfg.Config, db *store.DB, im *importer.Importer) *http.Server {
	mux := http.NewServeMux()
	s := &Server{cfg: c, db: db.DB, mux: mux, im: im}

	// helpers used by templates
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
	}

	// Parse base layout, then clone per page to avoid "content" conflicts
	base := template.Must(template.New("layout").Funcs(funcMap).ParseFS(tplFS, "views/layout.tmpl"))
	s.tplDash = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/dashboard.tmpl"))
	s.tplList = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/activities_list.tmpl"))
	s.tplDetail = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/activity_detail.tmpl"))
	s.tplStats = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/stats.tmpl"))
	s.tplImport = template.Must(template.Must(base.Clone()).ParseFS(tplFS, "views/import.tmpl"))

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
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/activities", s.handleActivities)
	mux.HandleFunc("/activity/", s.handleActivityDetail)
	mux.HandleFunc("/api/activity/", s.handleActivityGeoJSON)
	mux.HandleFunc("/api/import", s.handleImportNow) // POST
	mux.HandleFunc("/api/logs", s.handleLogsSSE)     // GET (SSE)
	mux.HandleFunc("/api/series/", s.handleActivitySeries)
	mux.HandleFunc("/api/zones/", s.handleActivityZones)
	mux.HandleFunc("/stats", s.handleStatsPage)
	mux.HandleFunc("/api/stats", s.handleStatsData)
	mux.HandleFunc("/api/stats/periods", s.handleStatsPeriods)
	mux.HandleFunc("/import", s.handleImportPage)
	mux.HandleFunc("/api/upload", s.handleFileUpload) // POST

	handler := http.Handler(mux)
	if strings.TrimSpace(c.AuthUser) != "" && strings.TrimSpace(c.AuthPass) != "" {
		handler = withBasicAuth(handler, c.AuthUser, c.AuthPass)
	}

	return &http.Server{Addr: c.HTTPAddr, Handler: handler}
}

func withBasicAuth(next http.Handler, username, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="garmr"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
