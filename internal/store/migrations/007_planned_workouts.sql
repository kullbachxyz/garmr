-- +goose Up
CREATE TABLE IF NOT EXISTS planned_workouts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  planned_date TEXT NOT NULL, -- YYYY-MM-DD (UTC)
  sport TEXT NOT NULL,
  title TEXT,
  distance_m INTEGER,
  duration_s INTEGER,
  notes TEXT,
  created_at DATETIME DEFAULT (datetime('now')),
  updated_at DATETIME DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS planned_workouts_date_idx ON planned_workouts(planned_date);

-- +goose Down
DROP TABLE IF EXISTS planned_workouts;
