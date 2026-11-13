CREATE SCHEMA IF NOT EXISTS poker_lab;
REVOKE ALL ON SCHEMA poker_lab FROM PUBLIC;

CREATE TABLE IF NOT EXISTS poker_lab.rooms (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS poker_lab.room_events (
    room_id text NOT NULL REFERENCES poker_lab.rooms(id) ON DELETE CASCADE,
    version bigint NOT NULL CHECK (version > 0),
    payload bytea NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, version)
);

CREATE TABLE IF NOT EXISTS poker_lab.chip_journal (
    room_id text NOT NULL,
    version bigint NOT NULL,
    ordinal smallint NOT NULL CHECK (ordinal >= 0),
    seat smallint NOT NULL CHECK (seat BETWEEN 0 AND 5),
    kind text NOT NULL CHECK (kind IN ('buy_in', 'rebuy', 'wager', 'award', 'refund')),
    amount bigint NOT NULL CHECK (amount > 0),
    PRIMARY KEY (room_id, version, ordinal),
    FOREIGN KEY (room_id, version) REFERENCES poker_lab.room_events(room_id, version) ON DELETE CASCADE
);

REVOKE ALL ON ALL TABLES IN SCHEMA poker_lab FROM PUBLIC;
