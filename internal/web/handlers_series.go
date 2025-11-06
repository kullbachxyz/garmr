package web

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// GET /api/series/{id}?width=900
// Returns time-bucketed series so the frontend draws fast without libs.
func (s *Server) handleActivitySeries(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/series/")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}

	// width → target number of points
	width := 900
	if q := r.URL.Query().Get("width"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 100 {
			width = v
		}
	}

	// duration_s from activities (fallback to last record if needed)
	var durS sql.NullInt64
	if err := s.db.QueryRow(`SELECT duration_s FROM activities WHERE id=?`, id).Scan(&durS); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !durS.Valid || durS.Int64 <= 0 {
		_ = s.db.QueryRow(`SELECT MAX(t_offset_s) FROM records WHERE activity_id=?`, id).Scan(&durS)
	}

	// Choose a bucket so we return roughly width..2*width points
	bucket := 1
	if durS.Valid && durS.Int64 > 0 {
		b := int(math.Ceil(float64(durS.Int64) / (float64(width) * 1.5)))
		if b < 1 {
			b = 1
		}
		bucket = b
	}

	// Bucketed query (SQLite integer math):
	// t_bin = floor(t_offset_s / bucket) * bucket
	rows, err := s.db.Query(`
		SELECT (t_offset_s / ?) * ? AS t_bin,
		       AVG(CASE WHEN hr != 255 THEN hr END) AS hr,
		       AVG(speed_mps) AS spd,
		       AVG(elev_m)    AS elev
		FROM records
		WHERE activity_id=?
		GROUP BY t_bin
		ORDER BY t_bin
	`, bucket, bucket, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type series struct {
		T    []int      `json:"t"`    // seconds since activity start
		HR   []any      `json:"hr"`   // []int or nulls
		Spd  []any      `json:"spd"`  // []float or nulls (m/s)
		Elev []any      `json:"elev"` // []float or nulls (m)
	}
	out := series{
		T:    make([]int, 0, width*2),
		HR:   make([]any, 0, width*2),
		Spd:  make([]any, 0, width*2),
		Elev: make([]any, 0, width*2),
	}

	for rows.Next() {
		var tbin int
		var hr, spd, elev sql.NullFloat64
		if err := rows.Scan(&tbin, &hr, &spd, &elev); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out.T = append(out.T, tbin)
		if hr.Valid {
			out.HR = append(out.HR, int(math.Round(hr.Float64)))
		} else {
			out.HR = append(out.HR, nil)
		}
		if spd.Valid {
			out.Spd = append(out.Spd, spd.Float64)
		} else {
			out.Spd = append(out.Spd, nil)
		}
		if elev.Valid {
			out.Elev = append(out.Elev, elev.Float64)
		} else {
			out.Elev = append(out.Elev, nil)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
