package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// --- Shared helpers/types ---

func daysInMonthUTC(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

const tsPrefix = "substr(start_time_utc,1,19)"

func toSQLite(ts time.Time) string {
	return ts.UTC().Format("2006-01-02 15:04:05")
}

type statsPageVM struct {
	MonthInput   string
	YearInput    string
	Sports       []string
	CurrentSport string
	CurrentUser  *userView
	Month        periodStats
	Year         periodStats
	MonthLabel   string
	YearLabel    string
	CurrentTab   string
}

type MonthItem struct {
	YM    string `json:"ym"`
	Label string `json:"label"`
}

type periodsOut struct {
	Years  []int       `json:"years"`
	Months []MonthItem `json:"months"`
}

// --- Handlers ---

func (s *Server) handleStatsPage(w http.ResponseWriter, r *http.Request) {
	sport := strings.TrimSpace(r.URL.Query().Get("sport"))
	tab := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tab")))
	if tab != "year" {
		tab = "month"
	}
	monthQS := strings.TrimSpace(r.URL.Query().Get("month"))
	yearQS := strings.TrimSpace(r.URL.Query().Get("year"))
	now := time.Now()
	loc := now.Location()

	// Build sports list from ALL activities
	sports := make([]string, 0, 8)
	rowsSports, err := s.db.Query(`
        SELECT DISTINCT TRIM(sport)
        FROM activities
        WHERE sport IS NOT NULL AND TRIM(sport) <> ''
    `)
	if err == nil {
		defer rowsSports.Close()
		seen := map[string]struct{}{}
		for rowsSports.Next() {
			var sp string
			if err := rowsSports.Scan(&sp); err == nil {
				key := strings.ToLower(strings.TrimSpace(sp))
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				sports = append(sports, sp)
			}
		}
	}
	// sort for consistent ordering
	for i := 0; i < len(sports); i++ {
		for j := i + 1; j < len(sports); j++ {
			if strings.ToLower(sports[i]) > strings.ToLower(sports[j]) {
				sports[i], sports[j] = sports[j], sports[i]
			}
		}
	}

	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	if t, err := time.Parse("2006-01", monthQS); err == nil {
		monthStart = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	}
	monthEnd := monthStart.AddDate(0, 1, 0)

	yearVal := now.Year()
	if yy, err := strconv.Atoi(yearQS); err == nil && yy > 0 {
		yearVal = yy
	}
	yearStart := time.Date(yearVal, 1, 1, 0, 0, 0, 0, loc)
	yearEnd := time.Date(yearVal+1, 1, 1, 0, 0, 0, 0, loc)

	vm := statsPageVM{
		MonthInput:   monthStart.Format("2006-01"),
		YearInput:    yearStart.Format("2006"),
		Sports:       sports,
		CurrentSport: sport,
		CurrentUser:  s.currentUser(r),
		CurrentTab:   tab,
	}

	monthStats, err := s.periodStatsFiltered(monthStart, monthEnd, sport)
	if err == nil {
		vm.Month = monthStats
		vm.MonthLabel = monthStart.Format("Jan 2006")
	}
	yearStats, err := s.periodStatsFiltered(yearStart, yearEnd, sport)
	if err == nil {
		vm.Year = yearStats
		vm.YearLabel = yearStart.Format("2006")
	}
	_ = s.tplStats.ExecuteTemplate(w, "layout", vm)
}

func (s *Server) handleStatsData(w http.ResponseWriter, r *http.Request) {
	gran := r.URL.Query().Get("gran")
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	sport := strings.TrimSpace(r.URL.Query().Get("sport"))

	type out struct {
		Labels    []string             `json:"labels"`
		KMs       []float64            `json:"kms"`
		Sports    map[string][]float64 `json:"sports,omitempty"`
		HasSports bool                 `json:"hasSports"`
		Summary   *periodStats         `json:"summary,omitempty"`
		Label     string               `json:"label,omitempty"`
	}
	resp := out{}

	switch gran {
	case "year":
		if year <= 0 {
			year = time.Now().Year()
		}
		loc := time.UTC
		yStart := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
		yEnd := yStart.AddDate(1, 0, 0)
		resp.Label = yStart.Format("2006")
		if s, err := s.periodStatsFiltered(yStart, yEnd, sport); err == nil {
			resp.Summary = &s
		}

		// Initialize labels
		for m := 1; m <= 12; m++ {
			resp.Labels = append(resp.Labels, time.Date(year, time.Month(m), 1, 0, 0, 0, 0, loc).Format("Jan"))
			resp.KMs = append(resp.KMs, 0)
		}

		if sport == "" {
			// Get sport breakdown data for colored visualization
			resp.HasSports = true
			resp.Sports = make(map[string][]float64)

			rows, err := s.db.Query(`
                SELECT strftime('%Y-%m', `+tsPrefix+`) AS ym, sport,
                       COALESCE(SUM(distance_m),0)
                FROM activities
                WHERE `+tsPrefix+` >= ? AND `+tsPrefix+` < ?
                  AND sport IS NOT NULL AND TRIM(sport) <> ''
                GROUP BY ym, sport
                ORDER BY ym, sport
            `, toSQLite(yStart), toSQLite(yEnd))
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			defer rows.Close()

			for rows.Next() {
				var ym, sportName string
				var distM float64
				if err := rows.Scan(&ym, &sportName, &distM); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				m, _ := strconv.Atoi(ym[5:7])
				if m >= 1 && m <= 12 {
					if resp.Sports[sportName] == nil {
						resp.Sports[sportName] = make([]float64, 12)
					}
					resp.Sports[sportName][m-1] = distM / 1000.0
					resp.KMs[m-1] += distM / 1000.0
				}
			}
		} else {
			// Single sport filter
			rows, err := s.db.Query(`
                SELECT strftime('%Y-%m', `+tsPrefix+`) AS ym,
                       COALESCE(SUM(distance_m),0)
                FROM activities
                WHERE `+tsPrefix+` >= ? AND `+tsPrefix+` < ? AND sport = ?
                GROUP BY ym
                ORDER BY ym
            `, toSQLite(yStart), toSQLite(yEnd), sport)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			defer rows.Close()

			for rows.Next() {
				var ym string
				var distM float64
				if err := rows.Scan(&ym, &distM); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				m, _ := strconv.Atoi(ym[5:7])
				if m >= 1 && m <= 12 {
					resp.KMs[m-1] = distM / 1000.0
				}
			}
		}

	default: // month
		yStr := strings.TrimSpace(r.URL.Query().Get("year"))
		mStr := strings.TrimSpace(r.URL.Query().Get("month"))

		var t time.Time
		if len(mStr) == 7 && strings.Count(mStr, "-") == 1 {
			if tt, err := time.Parse("2006-01", mStr); err == nil {
				t = tt
			}
		}
		if t.IsZero() {
			y, _ := strconv.Atoi(yStr)
			mm, _ := strconv.Atoi(mStr)
			if y > 0 && 1 <= mm && mm <= 12 {
				t = time.Date(y, time.Month(mm), 1, 0, 0, 0, 0, time.UTC)
			}
		}
		if t.IsZero() {
			now := time.Now().UTC()
			t = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		}

		mStart := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		mEnd := mStart.AddDate(0, 1, 0)
		resp.Label = mStart.Format("Jan 2006")
		if s, err := s.periodStatsFiltered(mStart, mEnd, sport); err == nil {
			resp.Summary = &s
		}

		ndays := daysInMonthUTC(mStart.Year(), mStart.Month())
		resp.Labels = make([]string, ndays)
		resp.KMs = make([]float64, ndays)
		for i := 1; i <= ndays; i++ {
			resp.Labels[i-1] = strconv.Itoa(i)
		}

		if sport == "" {
			// Get sport breakdown data for colored visualization
			resp.HasSports = true
			resp.Sports = make(map[string][]float64)

			rows, err := s.db.Query(`
                SELECT date(`+tsPrefix+`) AS d, sport,
                       COALESCE(SUM(distance_m),0)
                FROM activities
                WHERE `+tsPrefix+` >= ? AND `+tsPrefix+` < ?
                  AND sport IS NOT NULL AND TRIM(sport) <> ''
                GROUP BY d, sport
                ORDER BY d, sport
            `, toSQLite(mStart), toSQLite(mEnd))
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			defer rows.Close()

			for rows.Next() {
				var dayStr, sportName string
				var distM float64
				if err := rows.Scan(&dayStr, &sportName, &distM); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				if len(dayStr) >= 10 {
					if d, _ := strconv.Atoi(dayStr[8:10]); 1 <= d && d <= ndays {
						if resp.Sports[sportName] == nil {
							resp.Sports[sportName] = make([]float64, ndays)
						}
						resp.Sports[sportName][d-1] = distM / 1000.0
						resp.KMs[d-1] += distM / 1000.0
					}
				}
			}
		} else {
			// Single sport filter
			rows, err := s.db.Query(`
                SELECT date(`+tsPrefix+`) AS d,
                       COALESCE(SUM(distance_m),0)
                FROM activities
                WHERE `+tsPrefix+` >= ? AND `+tsPrefix+` < ? AND sport = ?
                GROUP BY d
                ORDER BY d
            `, toSQLite(mStart), toSQLite(mEnd), sport)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			defer rows.Close()

			for rows.Next() {
				var dayStr string
				var distM float64
				if err := rows.Scan(&dayStr, &distM); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				if len(dayStr) >= 10 {
					if d, _ := strconv.Atoi(dayStr[8:10]); 1 <= d && d <= ndays {
						resp.KMs[d-1] = distM / 1000.0
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleStatsPeriods(w http.ResponseWriter, r *http.Request) {
	yrows, err := s.db.Query(`
        SELECT DISTINCT strftime('%Y', ` + tsPrefix + `) AS y
        FROM activities
        ORDER BY y DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer yrows.Close()

	out := periodsOut{}
	for yrows.Next() {
		var ys string
		if err := yrows.Scan(&ys); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if len(ys) == 4 {
			yy, _ := strconv.Atoi(ys)
			out.Years = append(out.Years, yy)
		}
	}

	mrows, err := s.db.Query(`
        SELECT DISTINCT strftime('%Y-%m', ` + tsPrefix + `) AS ym
        FROM activities
        ORDER BY ym DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer mrows.Close()

	for mrows.Next() {
		var ym string
		if err := mrows.Scan(&ym); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		t, err := time.Parse("2006-01", ym)
		if err != nil {
			continue
		}
		out.Months = append(out.Months, MonthItem{
			YM: ym, Label: t.Format("Jan 2006"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
