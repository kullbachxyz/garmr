-- +goose Up
ALTER TABLE users ADD COLUMN theme TEXT NOT NULL DEFAULT 'system';
CREATE INDEX IF NOT EXISTS idx_users_theme ON users(theme);

-- +goose Down
DROP INDEX IF EXISTS idx_users_theme;
-- SQLite cannot drop columns; leave theme column in place on down migration.
