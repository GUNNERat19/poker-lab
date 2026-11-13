package live

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"cappedpoker/poker"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

type postgresStore struct {
	pool *pgxpool.Pool
	aead cipher.AEAD
}

// OpenPersistent restores room history before accepting requests.
func OpenPersistent(ctx context.Context, turnTime time.Duration, databaseURL, storageKey string) (*Server, error) {
	s := New(ctx, turnTime)
	startup, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	store, err := openStore(startup, databaseURL, storageKey)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.store = store
	states, err := store.load(startup)
	if err != nil {
		s.Close()
		return nil, err
	}
	for id, state := range states {
		state.ctx, state.id, state.store = s.ctx, id, store
		s.rooms[id] = startRoom(s.ctx, state)
	}
	return s, nil
}

func openStore(ctx context.Context, databaseURL, storageKey string) (*postgresStore, error) {
	key, err := base64.StdEncoding.DecodeString(storageKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("POKER_STORAGE_KEY must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("invalid POKER_DATABASE_URL")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("could not connect to PostgreSQL")
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("could not initialize the poker_lab schema: %w", err)
	}
	return &postgresStore{pool: pool, aead: aead}, nil
}

func eventKey(id string, version uint64) []byte {
	return fmt.Appendf(nil, "%s/%d", id, version)
}

func (s *postgresStore) append(ctx context.Context, id string, version uint64, event roomEvent, moves []chipMove) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	payload := s.aead.Seal(nil, nil, data, eventKey(id, version))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if version == 1 {
		if _, err := tx.Exec(ctx, "INSERT INTO poker_lab.rooms (id) VALUES ($1)", id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO poker_lab.room_events (room_id, version, payload) VALUES ($1, $2, $3)`, id, version, payload); err != nil {
		return err
	}
	for i, move := range moves {
		if _, err := tx.Exec(ctx, `INSERT INTO poker_lab.chip_journal (room_id, version, ordinal, seat, kind, amount)
			VALUES ($1, $2, $3, $4, $5, $6)`, id, version, i, move.seat, move.kind, move.amount); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *postgresStore) load(ctx context.Context) (map[string]*roomState, error) {
	rows, err := s.pool.Query(ctx, `SELECT room_id, version, payload FROM poker_lab.room_events ORDER BY room_id, version`)
	if err != nil {
		return nil, fmt.Errorf("load room events: %w", err)
	}
	defer rows.Close()

	states := make(map[string]*roomState)
	for rows.Next() {
		var id string
		var version uint64
		var payload []byte
		if err := rows.Scan(&id, &version, &payload); err != nil {
			return nil, fmt.Errorf("read room event: %w", err)
		}
		data, err := s.aead.Open(nil, nil, payload, eventKey(id, version))
		if err != nil {
			return nil, fmt.Errorf("decrypt room %s version %d: check POKER_STORAGE_KEY", id, version)
		}
		var event roomEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("invalid room %s event at version %d", id, version)
		}
		state := states[id]
		if state == nil {
			if version != 1 || event.Kind != "create" || event.Seat != 0 ||
				event.Config == nil || event.Rules != poker.RulesVersion || event.TurnTime <= 0 {
				return nil, fmt.Errorf("room %s has unsupported creation data", id)
			}
			state, err = initialState(*event.Config, event.TurnTime)
			if err != nil {
				return nil, fmt.Errorf("restore room %s: %w", id, err)
			}
			states[id] = state
		}
		if version != state.version+1 {
			return nil, fmt.Errorf("room %s is missing events before version %d", id, version)
		}
		if err := applyEvent(state.table, event); err != nil {
			return nil, fmt.Errorf("replay room %s version %d: %w", id, version, err)
		}
		state.accept(event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load room events: %w", err)
	}
	return states, nil
}
