package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"garmr/internal/store"
)

// If listItem already exists elsewhere, remove this.
type listItem struct {
	ID     int64
	Start  string // raw DB string; templates call trimUTC
	Sport  string
	DistKm float64
	DurS   int
}

type latestActivity struct {
	ID        int64
	Sport     string
	DistKm    float64
	DurS      int
	AvgSpd    float64
	Ago       string
	StartText string
}

type periodStats struct {
	DistM  float64
	DurS   int
	ElevM  float64
	Count  int
	AvgSpd float64 // m/s
}

type dashVM struct {
	Week         periodStats
	Month        periodStats
	Year         periodStats
	WeekLabel    string
	MonthLabel   string
	YearLabel    string
	Latest       []listItem
	LatestCard   *latestActivity
	Sports       []string // e.g. ["Run","Ride","Hike"]
	CurrentSport string   // "", "Run", etc. empty means “All”
	CurrentUser  *userView
}

type activitiesVM struct {
	Items        []listItem
	Sports       []string
	CurrentSport string
	CurrentUser  *userView
	Page         int
	TotalPages   int
	PageNumbers  []int
	HasPrev      bool
	HasNext      bool
	PrevPage     int
	NextPage     int
	PaginationQS string
}

type activityDetailVM struct {
	ID                              int64
	Start                           string
	Sport, Sub                      string
	DurS, DistM, AvgHR, MaxHR, Cals int
	AvgSpd, Asc, Dsc                float64
	AerobicTE, AnaerobicTE          sql.NullFloat64
	CurrentUser                     *userView
	HasHRData                       bool
}

type calendarEntry struct {
	ID       int64
	Sport    string
	DistKm   float64
	DurS     int
	Calories int
	Start    time.Time
}

type plannedEntry struct {
	ID     int64
	Sport  string
	Title  string
	DistKm float64
	DurS   int
	Notes  string
}

type calendarDay struct {
	Date    time.Time
	InMonth bool
	IsToday bool
	Items   []calendarEntry
	Planned []plannedEntry
	Totals  dayTotals
}

type calendarVM struct {
	Year          int
	Month         time.Month
	MonthLabel    string
	WeekLabel     string
	Grid          [][]calendarDay
	WeekDays      []calendarDay
	WeekTotals    weekTotals
	WeekRowTotals []weekTotals
	PrevURL       string
	NextURL       string
	TodayURL      string
	MonthLink     string
	WeekLink      string
	View          string
	Sports        []string
	CurrentUser   *userView
}

type weekTotals struct {
	DistM    float64
	DurS     int
	Calories int
	Count    int
}

type dayTotals struct {
	DistM    float64
	DurS     int
	Calories int
	Count    int
}

func nullableToKm(v sql.NullInt64) float64 {
	if v.Valid {
		return float64(v.Int64) / 1000.0
	}
	return 0
}

func nullableToInt(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return 0
}

const (
	activitiesPageSize  = 25
	paginationWindowLen = 7
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// ----- read filter from URL once -----
	sport := strings.TrimSpace(r.URL.Query().Get("sport")) // "" => All
	now := time.Now()
	loc := now.Location()

	// ----- recent activities (unfiltered; change if you want it filtered too) -----
	rows, err := s.db.Query(`
        SELECT id, start_time_utc, sport, distance_m, duration_s
        FROM activities
        ORDER BY start_time_utc DESC
        LIMIT 5`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var items []listItem
	for rows.Next() {
		var it listItem
		var distM, durS int
		if err := rows.Scan(&it.ID, &it.Start, &it.Sport, &distM, &durS); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		it.DistKm = float64(distM) / 1000.0
		it.DurS = durS
		items = append(items, it)
	}

	// ----- latest activity summary (unfiltered) -----
	var latestCard *latestActivity
	var startStr, sportName string
	var distM, durS int
	var avgSpd sql.NullFloat64
	var latestID int64
	err = s.db.QueryRow(`
        SELECT id, start_time_utc, sport, distance_m, duration_s, avg_speed_mps
        FROM activities
        ORDER BY start_time_utc DESC
        LIMIT 1`).Scan(&latestID, &startStr, &sportName, &distM, &durS, &avgSpd)
	switch err {
	case nil:
		startTime, _ := parseActivityTime(startStr)
		startLabel := strings.TrimSpace(strings.ReplaceAll(startStr, " +0000 UTC", ""))
		startLabel = strings.TrimSuffix(startLabel, " UTC")
		if !startTime.IsZero() {
			startLabel = startTime.UTC().Format("Mon 02 Jan 15:04")
		}
		avgSpdVal := avgSpd.Float64
		if !avgSpd.Valid && durS > 0 {
			avgSpdVal = float64(distM) / float64(durS)
		}
		latestCard = &latestActivity{
			ID:        latestID,
			Sport:     sportName,
			DistKm:    float64(distM) / 1000.0,
			DurS:      durS,
			AvgSpd:    avgSpdVal,
			Ago:       timeAgo(startTime, now),
			StartText: startLabel,
		}
	case sql.ErrNoRows:
	default:
		http.Error(w, err.Error(), 500)
		return
	}

	// ----- build sports list from ALL activities (unfiltered) -----
	sports := make([]string, 0, 8)
	rowsSports, err := s.db.Query(`
        SELECT DISTINCT TRIM(sport)
        FROM activities
        WHERE sport IS NOT NULL AND TRIM(sport) <> ''`)
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
	sort.Strings(sports)

	// ----- time windows -----
	ymd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	weekday := int(ymd.Weekday()) // Sun=0
	offset := (weekday + 6) % 7   // Mon=0
	weekStart := ymd.AddDate(0, 0, -offset)
	weekEnd := weekStart.AddDate(0, 0, 7)

	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	monthEnd := monthStart.AddDate(0, 1, 0)

	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
	yearEnd := time.Date(now.Year()+1, 1, 1, 0, 0, 0, 0, loc)

	// ----- aggregated stats (filtered by sport if provided) -----
	weekStats, err := s.periodStatsFiltered(weekStart, weekEnd, sport)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	monthStats, err := s.periodStatsFiltered(monthStart, monthEnd, sport)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	yearStats, err := s.periodStatsFiltered(yearStart, yearEnd, sport)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// ----- labels -----
	weekLabel := fmt.Sprintf("%s–%s", weekStart.Format("Mon 2"), weekEnd.AddDate(0, 0, -1).Format("Mon 2"))
	monthLabel := monthStart.Format("Jan 2006")
	yearLabel := yearStart.Format("2006")

	// ----- single render -----
	vm := dashVM{
		Week:         weekStats,
		Month:        monthStats,
		Year:         yearStats,
		WeekLabel:    weekLabel,
		MonthLabel:   monthLabel,
		YearLabel:    yearLabel,
		Latest:       items,
		LatestCard:   latestCard,
		Sports:       sports, // full, unfiltered list
		CurrentSport: sport,  // selected value
	}
	page := struct {
		dashVM
		CurrentUser *userView
	}{
		dashVM:      vm,
		CurrentUser: s.currentUser(r),
	}
	if err := s.tplDash.ExecuteTemplate(w, "layout", page); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func buildPageNumbers(current, total int) []int {
	if total <= 0 || current <= 0 {
		return nil
	}

	window := paginationWindowLen
	if total <= window {
		pages := make([]int, 0, total)
		for i := 1; i <= total; i++ {
			pages = append(pages, i)
		}
		return pages
	}

	start := current - window/2
	if start < 1 {
		start = 1
	}
	end := start + window - 1
	if end > total {
		end = total
		start = end - window + 1
	}

	pages := make([]int, 0, window)
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	return pages
}

func buildActivitiesRedirect(sport, page string) string {
	params := url.Values{}
	sport = strings.TrimSpace(sport)
	if sport != "" {
		params.Set("sport", sport)
	}
	page = strings.TrimSpace(page)
	if page != "" && page != "1" {
		if _, err := strconv.Atoi(page); err == nil {
			params.Set("page", page)
		}
	}
	target := "/activities"
	if qs := params.Encode(); qs != "" {
		target = target + "?" + qs
	}
	return target
}

// tiny iterator helper (optional)
func iterRows(rows *sql.Rows) <-chan *sql.Rows {
	ch := make(chan *sql.Rows)
	go func() {
		defer close(ch)
		for rows.Next() {
			ch <- rows
		}
	}()
	return ch
}

func (s *Server) periodStatsFiltered(from, to time.Time, sport string) (periodStats, error) {
	f := from.UTC().Format(time.RFC3339)
	t := to.UTC().Format(time.RFC3339)

	// Base WHERE by time; add sport if provided
	q := `
        SELECT
          COALESCE(SUM(distance_m), 0),
          COALESCE(SUM(duration_s), 0),
          COALESCE(SUM(ascent_m), 0),
          COUNT(*)
        FROM activities
        WHERE start_time_utc >= ? AND start_time_utc < ?
    `
	args := []any{f, t}
	if sport != "" {
		q += ` AND sport = ?`
		args = append(args, sport)
	}

	row := s.db.QueryRow(q, args...)
	var distM float64
	var durS int
	var elevM float64
	var count int
	if err := row.Scan(&distM, &durS, &elevM, &count); err != nil {
		return periodStats{}, err
	}
	ps := periodStats{
		DistM: distM,
		DurS:  durS,
		ElevM: elevM,
		Count: count,
	}
	if durS > 0 {
		ps.AvgSpd = distM / float64(durS)
	}
	return ps, nil
}

func parseActivityTime(ts string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", ts)
}

func timeAgo(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	if t.After(now) {
		return "just now"
	}
	diff := now.Sub(t)
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		m := int(diff.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d min ago", m)
	case diff < 24*time.Hour:
		h := int(diff.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case diff < 7*24*time.Hour:
		d := int(diff.Hours() / 24)
		if d == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", d)
	case diff < 30*24*time.Hour:
		w := int(diff.Hours() / (24 * 7))
		if w == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", w)
	case diff < 365*24*time.Hour:
		mo := int(diff.Hours() / (24 * 30))
		if mo == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", mo)
	default:
		y := int(diff.Hours() / (24 * 365))
		if y <= 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", y)
	}
}

func (s *Server) handleActivities(w http.ResponseWriter, r *http.Request) {
	sport := strings.TrimSpace(r.URL.Query().Get("sport")) // "" => All
	pageStr := strings.TrimSpace(r.URL.Query().Get("page"))
	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	// Build sports list from ALL activities (stable dropdown)
	sports := make([]string, 0, 8)
	rowsSports, err := s.db.Query(`
        SELECT DISTINCT TRIM(sport)
        FROM activities
        WHERE sport IS NOT NULL AND TRIM(sport) <> ''
    `)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rowsSports.Close()
	seen := map[string]struct{}{}
	for rowsSports.Next() {
		var sp string
		if err := rowsSports.Scan(&sp); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
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
	sort.Strings(sports)

	// Count total items for pagination
	var total int
	if sport == "" {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM activities`).Scan(&total)
	} else {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM activities WHERE sport = ?`, sport).Scan(&total)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + activitiesPageSize - 1) / activitiesPageSize
		if page > totalPages {
			page = totalPages
		}
	} else {
		page = 1
	}
	offset := (page - 1) * activitiesPageSize

	// Query items (optionally filtered by sport)
	var rows *sql.Rows
	if sport == "" {
		rows, err = s.db.Query(`
            SELECT id, start_time_utc, sport, distance_m, duration_s
            FROM activities
            ORDER BY start_time_utc DESC
            LIMIT ? OFFSET ?`, activitiesPageSize, offset)
	} else {
		rows, err = s.db.Query(`
            SELECT id, start_time_utc, sport, distance_m, duration_s
            FROM activities
            WHERE sport = ?
            ORDER BY start_time_utc DESC
            LIMIT ? OFFSET ?`, sport, activitiesPageSize, offset)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var items []listItem
	for rows.Next() {
		var it listItem
		var distM, durS int
		if err := rows.Scan(&it.ID, &it.Start, &it.Sport, &distM, &durS); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		it.DistKm = float64(distM) / 1000.0
		it.DurS = durS
		items = append(items, it)
	}

	params := url.Values{}
	if sport != "" {
		params.Set("sport", sport)
	}
	qs := params.Encode()
	if qs != "" {
		qs += "&"
	}
	pageNumbers := buildPageNumbers(page, totalPages)
	prevPage := 1
	if page > 1 {
		prevPage = page - 1
	}
	nextPage := page + 1

	vm := activitiesVM{
		Items:        items,
		Sports:       sports, // full unfiltered list
		CurrentSport: sport,  // selected value
		CurrentUser:  s.currentUser(r),
		Page:         page,
		TotalPages:   totalPages,
		PageNumbers:  pageNumbers,
		HasPrev:      page > 1,
		HasNext:      totalPages > 0 && page < totalPages,
		PrevPage:     prevPage,
		NextPage:     nextPage,
		PaginationQS: qs,
	}
	if err := s.tplList.ExecuteTemplate(w, "layout", vm); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func (s *Server) handleActivityDetail(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/activity/")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	row := s.db.QueryRow(`
        SELECT id, start_time_utc, sport, sub_sport, duration_s, distance_m,
               avg_hr, max_hr, avg_speed_mps, calories, ascent_m, descent_m,
               aerobic_te, anaerobic_te
        FROM activities WHERE id=?`, id)

	var vm activityDetailVM
	if err := row.Scan(&vm.ID, &vm.Start, &vm.Sport, &vm.Sub, &vm.DurS, &vm.DistM,
		&vm.AvgHR, &vm.MaxHR, &vm.AvgSpd, &vm.Cals, &vm.Asc, &vm.Dsc,
		&vm.AerobicTE, &vm.AnaerobicTE); err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var hrCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM records WHERE activity_id=? AND hr IS NOT NULL AND hr != 255`, id).Scan(&hrCount); err != nil {
		log.Printf("query hr presence for activity %d: %v", id, err)
	} else {
		vm.HasHRData = hrCount > 0
	}

	vm.CurrentUser = s.currentUser(r)
	_ = s.tplDetail.ExecuteTemplate(w, "layout", vm)
}

func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	loc := time.Local
	now := time.Now().In(loc)
	dayStart := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
	nowDay := dayStart(now)

	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if view != "month" {
		view = "week"
	}

	year := nowDay.Year()
	month := nowDay.Month()
	if yStr := strings.TrimSpace(r.URL.Query().Get("year")); yStr != "" {
		if y, err := strconv.Atoi(yStr); err == nil && y >= 2000 && y <= 2100 {
			year = y
		}
	}
	if mStr := strings.TrimSpace(r.URL.Query().Get("month")); mStr != "" {
		if m, err := strconv.Atoi(mStr); err == nil && m >= 1 && m <= 12 {
			month = time.Month(m)
		}
	}

	// Determine the anchor date for week mode
	weekAnchor := nowDay
	anchorFromQuery := false
	if dStr := strings.TrimSpace(r.URL.Query().Get("date")); dStr != "" {
		if t, err := time.Parse("2006-01-02", dStr); err == nil {
			weekAnchor = dayStart(t.In(loc))
			anchorFromQuery = true
		}
	}
	// If no explicit anchor in week view and the current week has no data, align to the latest activity
	if view == "week" && !anchorFromQuery {
		var latestStart string
		if err := s.db.QueryRow(`SELECT start_time_utc FROM activities ORDER BY start_time_utc DESC LIMIT 1`).Scan(&latestStart); err == nil {
			if t, err := parseActivityTime(latestStart); err == nil && !t.IsZero() {
				weekAnchor = dayStart(t.In(loc))
			}
		}
	}
	weekStart := weekAnchor.AddDate(0, 0, -((int(weekAnchor.Weekday()) + 6) % 7)) // Monday
	weekEnd := weekStart.AddDate(0, 0, 7)

	firstOfMonth := dayStart(time.Date(year, month, 1, 0, 0, 0, 0, loc))
	// For month view grid
	startOffset := (int(firstOfMonth.Weekday()) + 6) % 7
	gridStart := firstOfMonth.AddDate(0, 0, -startOffset)
	const gridDays = 42 // 6 weeks
	gridEnd := gridStart.AddDate(0, 0, gridDays)

	// Select range depending on view
	rangeStart := gridStart
	rangeEnd := gridEnd
	if view == "week" {
		rangeStart = weekStart
		rangeEnd = weekEnd
	}

	toDBTime := func(t time.Time) string {
		return t.UTC().Format("2006-01-02 15:04:05 -0700 MST")
	}
	rangeStartUTC := toDBTime(rangeStart)
	rangeEndUTC := toDBTime(rangeEnd)
	rows, err := s.db.Query(`
        SELECT id, start_time_utc, sport, distance_m, duration_s, calories
        FROM activities
        WHERE start_time_utc >= ? AND start_time_utc < ?
        ORDER BY start_time_utc ASC`, rangeStartUTC, rangeEndUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type dayKey string
	dayBuckets := make(map[dayKey][]calendarEntry)
	plannedBuckets := make(map[dayKey][]plannedEntry)

	for rows.Next() {
		var id int64
		var startStr, sport string
		var distM int
		var durS int
		var cals int
		if err := rows.Scan(&id, &startStr, &sport, &distM, &durS, &cals); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		startT, _ := parseActivityTime(startStr)
		startT = startT.In(loc)
		key := dayKey(dayStart(startT).Format("2006-01-02"))
		dayBuckets[key] = append(dayBuckets[key], calendarEntry{
			ID:       id,
			Sport:    sport,
			DistKm:   float64(distM) / 1000.0,
			DurS:     durS,
			Calories: cals,
			Start:    startT,
		})
	}
	// Planned workouts
	pRows, err := s.db.Query(`
        SELECT id, planned_date, sport, title, distance_m, duration_s, notes
        FROM planned_workouts
        WHERE planned_date >= ? AND planned_date < ?
        ORDER BY planned_date ASC`, rangeStart.Format("2006-01-02"), rangeEnd.Format("2006-01-02"))
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var id int64
			var dateStr, sport string
			var distM, durS sql.NullInt64
			var title, notes string
			if err := pRows.Scan(&id, &dateStr, &sport, &title, &distM, &durS, &notes); err != nil {
				continue
			}
			dt, err := time.Parse("2006-01-02", dateStr)
			if err != nil {
				continue
			}
			key := dayKey(dt.Format("2006-01-02"))
			plannedBuckets[key] = append(plannedBuckets[key], plannedEntry{
				ID:     id,
				Sport:  sport,
				Title:  title,
				DistKm: nullableToKm(distM),
				DurS:   int(nullableToInt(durS)),
				Notes:  notes,
			})
		}
	}

	today := nowDay
	isSameDay := func(a, b time.Time) bool {
		return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
	}

	// sports list for dropdown
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
		sort.Slice(sports, func(i, j int) bool {
			return strings.ToLower(sports[i]) < strings.ToLower(sports[j])
		})
	}

	vm := calendarVM{
		Year:        year,
		Month:       month,
		MonthLabel:  firstOfMonth.Format("January 2006"),
		View:        view,
		CurrentUser: s.currentUser(r),
		Sports:      sports,
	}

	if view == "month" {
		grid := make([][]calendarDay, 0, 6)
		weekRowTotals := make([]weekTotals, 0, 6)
		for week := 0; week < 6; week++ {
			row := make([]calendarDay, 0, 7)
			var rowTotals weekTotals
			for dow := 0; dow < 7; dow++ {
				dt := gridStart.AddDate(0, 0, week*7+dow)
				k := dayKey(dt.In(loc).Format("2006-01-02"))
				dayItems := dayBuckets[k]
				var totals dayTotals
				for _, it := range dayItems {
					totals.DistM += it.DistKm * 1000
					totals.DurS += it.DurS
					totals.Calories += it.Calories
					totals.Count++
					rowTotals.DistM += it.DistKm * 1000
					rowTotals.DurS += it.DurS
					rowTotals.Calories += it.Calories
					rowTotals.Count++
				}
				row = append(row, calendarDay{
					Date:    dt,
					InMonth: dt.Month() == firstOfMonth.Month(),
					IsToday: isSameDay(dt, today),
					Items:   dayItems,
					Planned: plannedBuckets[k],
					Totals:  totals,
				})
			}
			grid = append(grid, row)
			weekRowTotals = append(weekRowTotals, rowTotals)
		}
		prev := firstOfMonth.AddDate(0, -1, 0)
		next := firstOfMonth.AddDate(0, 1, 0)
		vm.Grid = grid
		vm.WeekRowTotals = weekRowTotals
		vm.PrevURL = fmt.Sprintf("/calendar?view=month&year=%d&month=%d", prev.Year(), int(prev.Month()))
		vm.NextURL = fmt.Sprintf("/calendar?view=month&year=%d&month=%d", next.Year(), int(next.Month()))
		vm.TodayURL = fmt.Sprintf("/calendar?view=month&year=%d&month=%d", nowDay.Year(), int(nowDay.Month()))
		vm.MonthLink = fmt.Sprintf("/calendar?view=month&year=%d&month=%d", year, int(month))
		vm.WeekLink = fmt.Sprintf("/calendar?view=week&date=%s", weekStart.Format("2006-01-02"))
	} else {
		weekDays := make([]calendarDay, 0, 7)
		var totals weekTotals
		for i := 0; i < 7; i++ {
			dt := weekStart.AddDate(0, 0, i)
			k := dayKey(dt.In(loc).Format("2006-01-02"))
			dayItems := dayBuckets[k]
			plannedItems := plannedBuckets[k]
			var dayTotal dayTotals
			for _, it := range dayItems {
				dayTotal.DistM += it.DistKm * 1000
				dayTotal.DurS += it.DurS
				dayTotal.Calories += it.Calories
				dayTotal.Count++
				totals.DistM += it.DistKm * 1000
				totals.DurS += it.DurS
				totals.Calories += it.Calories
				totals.Count++
			}
			weekDays = append(weekDays, calendarDay{
				Date:    dt,
				InMonth: true,
				IsToday: isSameDay(dt, today),
				Items:   dayItems,
				Planned: plannedItems,
				Totals:  dayTotal,
			})
		}
		prev := weekStart.AddDate(0, 0, -7)
		next := weekStart.AddDate(0, 0, 7)
		vm.WeekDays = weekDays
		vm.WeekTotals = totals
		vm.WeekLabel = fmt.Sprintf("%s – %s", weekStart.Format("Jan 2"), weekEnd.AddDate(0, 0, -1).Format("Jan 2"))
		vm.PrevURL = fmt.Sprintf("/calendar?view=week&date=%s", prev.Format("2006-01-02"))
		vm.NextURL = fmt.Sprintf("/calendar?view=week&date=%s", next.Format("2006-01-02"))
		vm.TodayURL = fmt.Sprintf("/calendar?view=week&date=%s", nowDay.Format("2006-01-02"))
		vm.WeekLink = fmt.Sprintf("/calendar?view=week&date=%s", weekStart.Format("2006-01-02"))
		vm.MonthLink = fmt.Sprintf("/calendar?view=month&year=%d&month=%d", weekStart.Year(), int(weekStart.Month()))
	}

	if err := s.tplCalendar.ExecuteTemplate(w, "layout", vm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCalendarPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	dateStr := strings.TrimSpace(r.FormValue("date"))
	sport := strings.TrimSpace(r.FormValue("sport"))
	title := strings.TrimSpace(r.FormValue("title"))
	distStr := strings.TrimSpace(r.FormValue("distance_km"))
	durStr := strings.TrimSpace(r.FormValue("duration_min"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	if dateStr == "" || sport == "" {
		http.Error(w, "date and sport required", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	var dist sql.NullInt64
	if distStr != "" {
		if v, err := strconv.ParseFloat(distStr, 64); err == nil && v >= 0 {
			dist.Valid = true
			dist.Int64 = int64(math.Round(v * 1000))
		}
	}
	var dur sql.NullInt64
	if durStr != "" {
		if v, err := strconv.Atoi(durStr); err == nil && v >= 0 {
			dur.Valid = true
			dur.Int64 = int64(v * 60)
		}
	}
	if _, err := s.store.InsertPlannedWorkout(date, sport, title, dist, dur, notes); err != nil {
		http.Error(w, "failed to save workout", http.StatusInternalServerError)
		return
	}

	redirect := "/calendar?view=week&date=" + url.QueryEscape(dateStr)
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleCalendarPlanUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	idStr := strings.TrimSpace(r.FormValue("id"))
	dateStr := strings.TrimSpace(r.FormValue("date"))
	sport := strings.TrimSpace(r.FormValue("sport"))
	distStr := strings.TrimSpace(r.FormValue("distance_km"))
	durStr := strings.TrimSpace(r.FormValue("duration_min"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	if sport == "" {
		http.Error(w, "sport required", http.StatusBadRequest)
		return
	}

	var dist sql.NullInt64
	if distStr != "" {
		if v, err := strconv.ParseFloat(distStr, 64); err == nil && v >= 0 {
			dist.Valid = true
			dist.Int64 = int64(math.Round(v * 1000))
		}
	}
	var dur sql.NullInt64
	if durStr != "" {
		if v, err := strconv.Atoi(durStr); err == nil && v >= 0 {
			dur.Valid = true
			dur.Int64 = int64(v * 60)
		}
	}

	if err := s.store.UpdatePlannedWorkout(id, date, sport, "", dist, dur, notes); err != nil {
		http.Error(w, "failed to update workout", http.StatusInternalServerError)
		return
	}

	redirect := "/calendar?view=week&date=" + url.QueryEscape(dateStr)
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleCalendarPlanDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	idStr := strings.TrimSpace(r.FormValue("id"))
	dateStr := strings.TrimSpace(r.FormValue("date"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if _, err := s.db.Exec(`DELETE FROM planned_workouts WHERE id = ?`, id); err != nil {
		http.Error(w, "failed to delete", http.StatusInternalServerError)
		return
	}
	redirect := "/calendar"
	if dateStr != "" {
		redirect = "/calendar?view=week&date=" + url.QueryEscape(dateStr)
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleActivityDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimSpace(r.URL.Query().Get("id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	rawPath, err := s.store.ActivityRawPath(id)
	switch err {
	case nil:
	case sql.ErrNoRows:
		http.NotFound(w, r)
		return
	default:
		http.Error(w, "failed to load activity", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(rawPath) == "" {
		http.NotFound(w, r)
		return
	}

	root, err := filepath.Abs(s.cfg.RawStore)
	if err != nil {
		http.Error(w, "invalid raw store path", http.StatusInternalServerError)
		return
	}
	target, err := filepath.Abs(rawPath)
	if err != nil {
		http.Error(w, "invalid file path", http.StatusInternalServerError)
		return
	}
	if !strings.HasPrefix(target, root+string(os.PathSeparator)) && target != root {
		target = filepath.Join(root, filepath.Clean(rawPath))
		if target, err = filepath.Abs(target); err != nil {
			http.Error(w, "invalid file path", http.StatusInternalServerError)
			return
		}
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		http.Error(w, "file not available", http.StatusNotFound)
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "file not available", http.StatusNotFound)
		return
	}

	filename := filepath.Base(target)
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = fmt.Sprintf("activity_%d.fit", id)
	}
	for strings.HasPrefix(filename, "upload_") {
		filename = strings.TrimPrefix(filename, "upload_")
	}
	// collapse repeated .fit suffixes to a single one
	base := filename
	for {
		lower := strings.ToLower(base)
		if strings.HasSuffix(lower, ".fit") {
			base = base[:len(base)-4]
			continue
		}
		break
	}
	base = strings.TrimRight(base, ".")
	if strings.TrimSpace(base) == "" {
		base = fmt.Sprintf("activity_%d", id)
	}
	filename = base + ".fit"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	http.ServeFile(w, r, target)
}

func (s *Server) handleActivityDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}
	idStr := strings.TrimSpace(r.FormValue("id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid activity id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteActivity(id); err != nil {
		log.Printf("delete activity %d: %v", id, err)
		http.Error(w, "failed to delete activity", http.StatusInternalServerError)
		return
	}

	redirect := buildActivitiesRedirect(r.FormValue("sport"), r.FormValue("page"))
	if ret := strings.TrimSpace(r.FormValue("return_to")); ret != "" && strings.HasPrefix(ret, "/") {
		redirect = ret
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleActivityGeoJSON(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/activity/")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	rows, err := s.db.Query(`
        SELECT t_offset_s, lat_deg, lon_deg, hr, speed_mps
        FROM records
        WHERE activity_id = ?
          AND lat_deg IS NOT NULL
          AND lon_deg IS NOT NULL
        ORDER BY t_offset_s ASC`, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type pt [2]float64 // [lon, lat]
	type richPt struct {
		Lon float64  `json:"lon"`
		Lat float64  `json:"lat"`
		T   int      `json:"t"`
		HR  *int     `json:"hr,omitempty"`
		Spd *float64 `json:"spd,omitempty"`
	}
	var coords []pt
	var points []richPt

	haversineM := func(lat1, lon1, lat2, lon2 float64) float64 {
		const R = 6371000.0
		toRad := func(d float64) float64 { return d * math.Pi / 180 }
		dlat := toRad(lat2 - lat1)
		dlon := toRad(lon2 - lon1)
		a := math.Sin(dlat/2)*math.Sin(dlat/2) +
			math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dlon/2)*math.Sin(dlon/2)
		c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
		return R * c
	}

	var lastLat, lastLon float64
	var lastT int
	const (
		epsDeg     = 1e-7
		maxSpeedMS = 12.0
		maxJumpM   = 5000.0
	)

	kept := 0
	for rows.Next() {
		var t int
		var lat, lon sql.NullFloat64
		var hr sql.NullInt64
		var spd sql.NullFloat64
		if err := rows.Scan(&t, &lat, &lon, &hr, &spd); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !lat.Valid || !lon.Valid {
			continue
		}
		la, lo := lat.Float64, lon.Float64
		if la < -90 || la > 90 || lo < -180 || lo > 180 {
			continue
		}
		if la == 0 || lo == 0 {
			continue
		}

		if kept > 0 {
			if math.Abs(lastLat-la) < epsDeg && math.Abs(lastLon-lo) < epsDeg {
				continue
			}
			dt := t - lastT
			if dt <= 0 {
				continue
			}
			d := haversineM(lastLat, lastLon, la, lo)
			if d > maxJumpM {
				continue
			}
			if d/float64(dt) > maxSpeedMS {
				continue
			}
		}

		coords = append(coords, pt{lo, la})
		var hrPtr *int
		if hr.Valid && hr.Int64 != 255 {
			val := int(hr.Int64)
			hrPtr = &val
		}
		var spdPtr *float64
		if spd.Valid {
			v := spd.Float64
			if v > 0 {
				spdPtr = &v
			}
		}
		points = append(points, richPt{Lon: lo, Lat: la, T: t, HR: hrPtr, Spd: spdPtr})
		lastLat, lastLon, lastT = la, lo, t
		kept++
	}

	if kept < 2 {
		log.Printf("geojson: activity %d -> %d points after filtering (nothing to draw)", id, kept)
	} else {
		log.Printf("geojson: activity %d -> %d points", id, kept)
	}

	feat := map[string]any{
		"type": "Feature",
		"geometry": map[string]any{
			"type":        "LineString",
			"coordinates": coords,
		},
		"properties": map[string]any{
			"points": points,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(feat)
}

func (s *Server) handleActivityZones(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/zones/")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	db := &store.DB{DB: s.db}
	zones, err := db.GetHRZones(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(zones)
}

func (s *Server) periodStats(from time.Time, to time.Time) (periodStats, error) {
	// stored as TEXT; we compare with ISO8601
	f := from.UTC().Format(time.RFC3339)
	t := to.UTC().Format(time.RFC3339)
	row := s.db.QueryRow(`
        SELECT
          COALESCE(SUM(distance_m), 0),
          COALESCE(SUM(duration_s), 0),
          COALESCE(SUM(ascent_m), 0),
          COUNT(*)
        FROM activities
        WHERE start_time_utc >= ? AND start_time_utc < ?
    `, f, t)

	var distM float64
	var durS int
	var elevM float64
	var count int
	if err := row.Scan(&distM, &durS, &elevM, &count); err != nil {
		return periodStats{}, err
	}
	ps := periodStats{
		DistM: distM,
		DurS:  durS,
		ElevM: elevM,
		Count: count,
	}
	if durS > 0 {
		ps.AvgSpd = distM / float64(durS)
	}
	return ps, nil
}
