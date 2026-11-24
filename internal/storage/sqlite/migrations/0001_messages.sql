CREATE TABLE history_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    owner BLOB NOT NULL CHECK (length(owner) = 32),
    salt BLOB NOT NULL CHECK (length(salt) = 32),
    key_check BLOB NOT NULL CHECK (length(key_check) = 40)
) STRICT;

CREATE TABLE messages (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    peer BLOB NOT NULL CHECK (length(peer) = 32),
    direction INTEGER NOT NULL CHECK (direction IN (1, 2)),
    message_id BLOB NOT NULL CHECK (length(message_id) = 16),
    recorded_at INTEGER NOT NULL CHECK (recorded_at BETWEEN 0 AND 253402300799999),
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) BETWEEN 41 AND 16424),
    UNIQUE (peer, direction, message_id)
) STRICT;

CREATE INDEX messages_peer_sequence ON messages (peer, sequence DESC);
