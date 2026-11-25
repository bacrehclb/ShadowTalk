CREATE TABLE outbox (
    sequence INTEGER PRIMARY KEY REFERENCES messages(sequence) ON DELETE RESTRICT,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 1000000000),
    next_attempt INTEGER NOT NULL CHECK (next_attempt BETWEEN 0 AND 253402300799999),
    delivered_at INTEGER CHECK (delivered_at BETWEEN 0 AND 253402300799999)
) STRICT;

CREATE INDEX outbox_due ON outbox (next_attempt, sequence) WHERE delivered_at IS NULL;
