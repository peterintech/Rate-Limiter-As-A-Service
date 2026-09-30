-- +goose Up
CREATE TABLE approved_requests (
    stream_id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    resource TEXT NOT NULL,
    cost INTEGER NOT NULL CHECK (cost > 0),
    approved_at TIMESTAMPTZ NOT NULL,
    remaining INTEGER NOT NULL CHECK (remaining >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX approved_requests_approved_at_idx
    ON approved_requests (approved_at);

-- +goose Down
DROP TABLE approved_requests;
