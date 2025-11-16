package web

import (
	"database/sql"
	"encoding/json"
	"fmt" // + add
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time" // + add

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
}

const (
	activitiesPageSize  = 25
	paginationWindowLen = 7
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// ----- read filter from URL once -----
	sport := strings.TrimSpace(r.URL.Query().Get("sport")) // "" => All

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
	now := time.Now()
	loc := now.Location()
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

	vm.CurrentUser = s.currentUser(r)
	_ = s.tplDetail.ExecuteTemplate(w, "layout", vm)
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
        SELECT t_offset_s, lat_deg, lon_deg
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
	var coords []pt

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
		if err := rows.Scan(&t, &lat, &lon); err != nil {
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

		if kept == 0 {
			coords = append(coords, pt{lo, la})
			lastLat, lastLon, lastT = la, lo, t
			kept++
			continue
		}

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

		coords = append(coords, pt{lo, la})
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
		"properties": map[string]any{},
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
