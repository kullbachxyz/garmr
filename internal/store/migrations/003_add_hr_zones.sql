-- +goose Up
CREATE TABLE hr_zones (
    activity_id INTEGER NOT NULL,
    zone INTEGER NOT NULL,
    time_seconds INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (activity_id) REFERENCES activities (id) ON DELETE CASCADE,
    PRIMARY KEY (activity_id, zone)
);

-- +goose Down
DROP TABLE hr_zones;