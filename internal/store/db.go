package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"garmr/internal/fitx"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

type DB struct{ *sql.DB }

type Activity struct {
	ID           int64
	FitUID       string
	StartTimeUTC time.Time
	Sport        string
	SubSport     string
	DurationS    int
	DistanceM    int
	AvgHR        int
	MaxHR        int
	AvgSpeedMPS  float64
	Calories     int
	AscentM      float64
	DescentM     float64
	DeviceVendor string
	DeviceModel  string
	RawPath      string
	// Training effects (Garmin specific)
	AerobicTE   sql.NullFloat64 // Aerobic Training Effect (0.0-5.0)
	AnaerobicTE sql.NullFloat64 // Anaerobic Training Effect (0.0-5.0)
}

type Record struct {
	TOffsetS        int
	Lat, Lon, ElevM sql.NullFloat64
	HR, Cad         sql.NullInt64
	TempC           sql.NullFloat64
	PowerW          sql.NullInt64
	SpeedMPS        sql.NullFloat64
}

type Lap struct {
	Index, StartOff, DurS, DistM, AvgHR, MaxHR int
	AvgSpd                                     float64
}

type HRZone struct {
	Zone        int `json:"zone"`
	TimeSeconds int `json:"time_seconds"`
}

func Open(path string) (*DB, error) {
	// ensure parent directory exists (SQLite won't create parents)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// modernc.org/sqlite DSN: enable FKs, set busy timeout, shared cache, read-write-create
	dsn := fmt.Sprintf("file:%s?cache=shared&_fk=1&_busy_timeout=8000&mode=rwc", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	return &DB{db}, nil
}

func (db *DB) WithTx(fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func Migrate(db *DB) error {
	goose.SetBaseFS(embedMigrations)
	goose.SetDialect("sqlite3")
	return goose.Up(db.DB, "migrations")
}

func (db *DB) LookupActivityByUID(tx *sql.Tx, uid string) (int64, error) {
	var id int64
	err := tx.QueryRow("SELECT id FROM activities WHERE fit_uid=?", uid).Scan(&id)
	return id, err
}
func (db *DB) LookupActivityByHash(tx *sql.Tx, h string) (int64, error) {
	var id int64
	err := tx.QueryRow("SELECT id FROM activities WHERE file_hash=?", h).Scan(&id)
	return id, err
}

func (db *DB) ActivityRawPath(id int64) (string, error) {
	var path string
	err := db.QueryRow(`SELECT raw_path FROM activities WHERE id = ?`, id).Scan(&path)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (db *DB) InsertActivity(tx *sql.Tx, a fitx.Activity, rawPath, hash string) (int64, error) {
	res, err := tx.Exec(`INSERT INTO activities(
		fit_uid,start_time_utc,sport,sub_sport,duration_s,distance_m,avg_hr,max_hr,avg_speed_mps,calories,ascent_m,descent_m,device_vendor,device_model,raw_path,file_hash,aerobic_te,anaerobic_te,created_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,datetime('now'))`,
		a.FitUID, a.StartTimeUTC, a.Sport, a.SubSport, a.DurationS, a.DistanceM, a.AvgHR, a.MaxHR, a.AvgSpeedMPS, a.Calories, a.AscentM, a.DescentM, a.DeviceVendor, a.DeviceModel, rawPath, hash, a.AerobicTE, a.AnaerobicTE)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) InsertRecords(tx *sql.Tx, id int64, recs []fitx.Record) error {
	stmt, err := tx.Prepare(`INSERT INTO records(activity_id,t_offset_s,lat_deg,lon_deg,elev_m,hr,cad,temp_c,power_w,speed_mps) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range recs {
		var lat, lon, elev, temp, spd interface{}
		var hr, cad, pwr interface{}
		if r.Lat != nil {
			lat = *r.Lat
		}
		if r.Lon != nil {
			lon = *r.Lon
		}
		if r.ElevM != nil {
			elev = *r.ElevM
		}
		if r.TempC != nil {
			temp = *r.TempC
		}
		if r.SpeedMPS != nil {
			spd = *r.SpeedMPS
		}
		if r.HR != nil {
			hr = *r.HR
		}
		if r.Cad != nil {
			cad = *r.Cad
		}
		if r.PowerW != nil {
			pwr = *r.PowerW
		}
		if _, err := stmt.Exec(id, r.TOffsetS, lat, lon, elev, hr, cad, temp, pwr, spd); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) InsertLaps(tx *sql.Tx, id int64, laps []fitx.Lap) error {
	stmt, err := tx.Prepare(`INSERT INTO laps(activity_id,lap_index,start_offset_s,duration_s,distance_m,avg_hr,max_hr,avg_speed_mps) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, l := range laps {
		if _, err := stmt.Exec(id, l.Index, l.StartOff, l.DurS, l.DistM, l.AvgHR, l.MaxHR, l.AvgSpd); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) UpsertDailyAgg(tx *sql.Tx, start time.Time, a fitx.Activity) error {
	day := start.UTC().Format("2006-01-02")
	_, err := tx.Exec(`INSERT INTO agg_daily(day,total_distance_m,total_duration_s,total_elev_m,total_calories,runs,rides)
	VALUES(?,?,?,?,?,?,?)
	ON CONFLICT(day) DO UPDATE SET
		total_distance_m = total_distance_m + excluded.total_distance_m,
		total_duration_s = total_duration_s + excluded.total_duration_s,
		total_elev_m = total_elev_m + excluded.total_elev_m,
		total_calories = total_calories + excluded.total_calories,
		runs = runs + excluded.runs,
		rides = rides + excluded.rides
	`, day, a.DistanceM, a.DurationS, int(a.AscentM), a.Calories, boolToInt(a.Sport == "running"), boolToInt(a.Sport == "cycling"))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (db *DB) InsertHRZones(tx *sql.Tx, activityID int64, zones []HRZone) error {
	stmt, err := tx.Prepare(`INSERT INTO hr_zones(activity_id, zone, time_seconds) VALUES(?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, zone := range zones {
		if _, err := stmt.Exec(activityID, zone.Zone, zone.TimeSeconds); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) GetHRZones(activityID int64) ([]HRZone, error) {
	rows, err := db.Query(`SELECT zone, time_seconds FROM hr_zones WHERE activity_id = ? ORDER BY zone`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var zones []HRZone
	for rows.Next() {
		var zone HRZone
		if err := rows.Scan(&zone.Zone, &zone.TimeSeconds); err != nil {
			return nil, err
		}
		zones = append(zones, zone)
	}
	return zones, nil
}

func (db *DB) CalculateHRZonesForActivity(activityID int64, maxHR int) error {
	if maxHR == 0 {
		return nil // Can't calculate zones without max HR
	}

	// Get HR records for this activity (exclude 255 which is invalid sensor data)
	rows, err := db.Query(`
		SELECT t_offset_s, hr
		FROM records
		WHERE activity_id = ? AND hr IS NOT NULL AND hr != 255
		ORDER BY t_offset_s`, activityID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type hrRecord struct {
		TOffsetS int
		HR       int
	}

	var records []hrRecord
	for rows.Next() {
		var r hrRecord
		if err := rows.Scan(&r.TOffsetS, &r.HR); err != nil {
			return err
		}
		records = append(records, r)
	}

	if len(records) < 2 {
		return nil // Need at least 2 records to calculate durations
	}

	// Standard HR zone thresholds (percentage of max HR)
	zoneThresholds := []float64{
		float64(maxHR) * 0.50, // Zone 1 lower bound
		float64(maxHR) * 0.60, // Zone 2 lower bound
		float64(maxHR) * 0.70, // Zone 3 lower bound
		float64(maxHR) * 0.80, // Zone 4 lower bound
		float64(maxHR) * 0.90, // Zone 5 lower bound
		float64(maxHR) * 1.00, // Zone 5 upper bound
	}

	// Count time in each zone
	zoneTimes := make([]int, 5) // 5 zones

	for i := 0; i < len(records)-1; i++ {
		curr := records[i]
		next := records[i+1]

		hr := float64(curr.HR)
		duration := next.TOffsetS - curr.TOffsetS

		// Determine which zone this HR falls into
		var zone int = -1
		for z := 0; z < 5; z++ {
			if hr >= zoneThresholds[z] && hr < zoneThresholds[z+1] {
				zone = z
				break
			}
		}

		if zone >= 0 {
			zoneTimes[zone] += duration
		}
	}

	// Insert zone data (delete existing first)
	return db.WithTx(func(tx *sql.Tx) error {
		// Delete existing HR zones for this activity
		if _, err := tx.Exec(`DELETE FROM hr_zones WHERE activity_id = ?`, activityID); err != nil {
			return err
		}

		// Insert new zone data
		stmt, err := tx.Prepare(`INSERT INTO hr_zones(activity_id, zone, time_seconds) VALUES(?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for zone, timeSeconds := range zoneTimes {
			if timeSeconds > 0 {
				if _, err := stmt.Exec(activityID, zone+1, timeSeconds); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (db *DB) DeleteActivity(id int64) error {
	_, err := db.Exec(`DELETE FROM activities WHERE id = ?`, id)
	return err
}
