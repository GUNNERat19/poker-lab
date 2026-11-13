package live

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"cappedpoker/poker"

	"github.com/ethereum/go-ethereum/common"
)

const receiptLimit = 128
const maxConnections = 32

type command struct {
	ID        string           `json:"id"`
	Version   uint64           `json:"version"`
	Kind      string           `json:"kind"`
	Action    poker.ActionKind `json:"action,omitempty"`
	Target    poker.Chips      `json:"target,omitempty"`
	Sequence  uint64           `json:"sequence,omitempty"`
	Signature string           `json:"signature,omitempty"`
}

type receipt struct {
	command command
	version uint64
}

type player struct {
	credential string
	receipts   []receipt
}

type subscription struct {
	seat   int
	states chan snapshot
}

type roomState struct {
	ctx             context.Context
	id              string
	scenario        string
	store           *postgresStore
	storagePaused   bool
	table           *poker.Table
	version         uint64
	turnTime        time.Duration
	deadline        time.Time
	players         []player
	subscribers     map[chan snapshot]int
	escrowSource    *escrowSource
	escrow          *escrowSession
	bound, saved    []bool
	pending, latest *checkpoint
	transcript      common.Hash
}

type room struct {
	requests chan func(*roomState)
	done     chan struct{}
}

func secret(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func newRoom(ctx context.Context, id string, turnTime time.Duration, token string, store *postgresStore, scenario string, source *escrowSource) (*room, error) {
	config := poker.DefaultConfig()
	if scenario == sidePotPractice {
		config.Seats, config.BuyIn = 3, 10
	}
	if source != nil {
		config.Seats = len(source.Roster)
	}
	state, err := initialState(config, turnTime)
	if err != nil {
		return nil, err
	}
	state.ctx, state.id, state.store = ctx, id, store
	event := roomEvent{Kind: "create", Seat: 0, Rules: poker.RulesVersion, Config: &config, TurnTime: turnTime, Credential: credential(token), Scenario: scenario}
	if source != nil {
		state.escrowSource, event.Escrow = source, &source.escrowSession
	}
	if err := state.commit(event); err != nil {
		return nil, err
	}
	return startRoom(ctx, state), nil
}

func startRoom(ctx context.Context, state *roomState) *room {
	r := &room{requests: make(chan func(*roomState)), done: make(chan struct{})}
	go r.run(ctx, state)
	return r
}

func (r *room) run(ctx context.Context, state *roomState) {
	defer close(r.done)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		timer.Stop()
		var alarm <-chan time.Time
		if !state.storagePaused && !state.deadline.IsZero() {
			timer.Reset(time.Until(state.deadline))
			alarm = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-alarm:
			state.expireTurn()
		case request := <-r.requests:
			state.expireTurn()
			request(state)
		}
	}
}

func ask[T any](ctx context.Context, r *room, fn func(*roomState) T) (T, error) {
	reply := make(chan T, 1)
	var zero T
	select {
	case r.requests <- func(s *roomState) { reply <- fn(s) }:
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-r.done:
		return zero, errors.New("room closed")
	}
	select {
	case result := <-reply:
		return result, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (s *roomState) seatFor(token string) int {
	if token != "" {
		digest := credential(token)
		for i, player := range s.players {
			if player.credential == digest {
				return i
			}
		}
	}
	return poker.NoSeat
}

type entry struct {
	seat  int
	token string
	err   error
}

func (s *roomState) join(token string, preferred int, proof string) entry {
	if seat := s.seatFor(token); seat != poker.NoSeat {
		return entry{seat: seat, token: token}
	}
	if s.escrow != nil && (preferred < 0 || preferred >= len(s.players) ||
		!matchesWallet(s.escrow.Roster[preferred], s.escrow.bindingDigest(hashText(s.id), preferred), proof)) {
		return entry{err: errors.New("sign with the wallet assigned to the open seat")}
	}
	for seat, player := range s.players {
		if player.credential != "" || (preferred != poker.NoSeat && preferred != seat) {
			continue
		}
		token, err := secret(32)
		if err != nil {
			return entry{err: err}
		}
		if err := s.commit(roomEvent{Kind: "join", Seat: seat, Credential: credential(token), Binding: proof}); err != nil {
			return entry{err: err}
		}
		return entry{seat: seat, token: token}
	}
	if preferred != poker.NoSeat {
		return entry{err: errors.New("that seat is unavailable")}
	}
	return entry{err: errors.New("all seats are occupied")}
}

func (s *roomState) subscribe(token string) subscription {
	if len(s.subscribers) >= maxConnections {
		return subscription{}
	}
	seat := s.seatFor(token)
	ch := make(chan snapshot, 1)
	s.subscribers[ch] = seat
	ch <- s.snapshot(seat)
	return subscription{seat: seat, states: ch}
}

func (s *roomState) broadcast() {
	for ch, seat := range s.subscribers {
		select {
		case <-ch:
		default:
		}
		ch <- s.snapshot(seat)
	}
}

func (s *roomState) expireTurn() {
	if s.storagePaused || s.deadline.IsZero() || time.Now().Before(s.deadline) {
		return
	}
	actor := s.table.View(poker.NoSeat).Actor
	if err := s.commit(roomEvent{Kind: "timeout", Seat: actor}); err != nil && !s.storagePaused {
		// Avoid retrying a deadline if the engine no longer has that turn.
		s.deadline = time.Time{}
	}
}

func (s *roomState) apply(seat int, cmd command) ack {
	result := ack{Type: "ack", ID: cmd.ID, Version: s.version}
	fail := func(message string) ack {
		result.Error = message
		return result
	}
	if seat == poker.NoSeat {
		return fail("Join the table to act.")
	}
	if len(cmd.ID) == 0 || len(cmd.ID) > 64 {
		return fail("Invalid action ID.")
	}
	p := &s.players[seat]
	for _, old := range p.receipts {
		if old.command.ID != cmd.ID {
			continue
		}
		if old.command != cmd {
			return fail("Action ID already used.")
		}
		result.Version = old.version
		return result
	}
	walletCommand := cmd.Kind == "bind-wallet" || cmd.Kind == "sign-checkpoint" || cmd.Kind == "save-checkpoint"
	if cmd.Version != s.version && (!walletCommand || cmd.Version == 0 || cmd.Version > s.version) {
		return fail("The table has moved on. Use the latest controls.")
	}
	if cmd.Kind != "act" && (cmd.Action != "" || cmd.Target != 0) {
		return fail("Unexpected action fields.")
	}
	if !walletCommand && (cmd.Signature != "" || cmd.Sequence != 0) {
		return fail("Unexpected checkpoint fields.")
	}
	if s.storagePaused {
		return fail(errStoragePaused.Error())
	}
	event := roomEvent{Kind: cmd.Kind, Seat: seat, Command: &cmd}
	switch cmd.Kind {
	case "bind-wallet", "sign-checkpoint", "save-checkpoint":
		if err := s.escrowCommand(seat, cmd); err != nil {
			return fail(err.Error())
		}
	case "deal":
		if seat != 0 {
			return fail("Only the host can deal.")
		}
		view := s.table.View(poker.NoSeat)
		if view.Street != poker.Waiting && view.Street != poker.Settled {
			return fail("Finish the current hand before dealing.")
		}
		if !s.enoughToDeal(view) {
			if s.scenario == sidePotPractice && view.Hand < 2 {
				return fail("Seat three funded players to start this practice hand.")
			}
			return fail(poker.ErrPlayers.Error())
		}
		if !s.escrowReady() {
			return fail("Every seat must bind its wallet and save the latest signed checkpoint before dealing.")
		}
		if s.escrowSource != nil {
			if err := s.escrowSource.requireActive(s.ctx); err != nil {
				return fail(err.Error())
			}
		}
		deck, shuffleErr := s.nextDeck(view.Hand)
		if shuffleErr != nil {
			return fail("Could not shuffle the deck.")
		}
		event.Deck = &deck
	case "rebuy":
		if s.escrow != nil {
			return fail("A fresh escrow session is required to add chips.")
		}
	case "act":
	default:
		return fail("Unknown command.")
	}
	if err := s.commit(event); err != nil {
		return fail(err.Error())
	}
	result.Version = s.version
	return result
}
