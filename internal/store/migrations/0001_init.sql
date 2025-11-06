-- +goose Up
CREATE TABLE IF NOT EXISTS activities (
	id INTEGER PRIMARY KEY,
	fit_uid TEXT,
	start_time_utc TEXT NOT NULL,
	sport TEXT, sub_sport TEXT,
	duration_s INTEGER, distance_m INTEGER,
	avg_hr INTEGER, max_hr INTEGER,
	avg_speed_mps REAL,
	calories INTEGER,
	ascent_m REAL, descent_m REAL,
	device_vendor TEXT, device_model TEXT,
	raw_path TEXT NOT NULL,
	file_hash TEXT,
	created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_activities_fituid ON activities(fit_uid);
CREATE UNIQUE INDEX IF NOT EXISTS uidx_activities_filehash ON activities(file_hash);
CREATE INDEX IF NOT EXISTS idx_activities_start ON activities(start_time_utc);

CREATE TABLE IF NOT EXISTS records (
	activity_id INTEGER NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
	t_offset_s INTEGER NOT NULL,
	lat_deg REAL, lon_deg REAL, elev_m REAL,
	hr INTEGER, cad INTEGER, temp_c REAL, power_w INTEGER, speed_mps REAL
);
CREATE INDEX IF NOT EXISTS idx_records_activity ON records(activity_id);

CREATE TABLE IF NOT EXISTS laps (
	activity_id INTEGER NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
	lap_index INTEGER, start_offset_s INTEGER, duration_s INTEGER,
	distance_m INTEGER, avg_hr INTEGER, max_hr INTEGER, avg_speed_mps REAL
);

CREATE TABLE IF NOT EXISTS agg_daily (
	day TEXT PRIMARY KEY,
	total_distance_m INTEGER DEFAULT 0,
	total_duration_s INTEGER DEFAULT 0,
	total_elev_m INTEGER DEFAULT 0,
	total_calories INTEGER DEFAULT 0,
	runs INTEGER DEFAULT 0,
	rides INTEGER DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS agg_daily;
DROP TABLE IF EXISTS laps;
DROP TABLE IF EXISTS records;
DROP TABLE IF EXISTS activities;