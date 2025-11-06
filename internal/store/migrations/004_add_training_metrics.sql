-- +goose Up
-- +goose StatementBegin
ALTER TABLE activities ADD COLUMN aerobic_te REAL;
ALTER TABLE activities ADD COLUMN anaerobic_te REAL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE activities DROP COLUMN aerobic_te;
ALTER TABLE activities DROP COLUMN anaerobic_te;
-- +goose StatementEnd