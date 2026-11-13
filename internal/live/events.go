package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"time"

	"cappedpoker/poker"
)

var errStoragePaused = errors.New("Room storage is unavailable. Restart the server to recover the room.")

type roomEvent struct {
	Kind       string         `json:"kind"`
	Seat       int            `json:"seat"`
	Credential string         `json:"credential,omitempty"`
	Rules      string         `json:"rules,omitempty"`
	Scenario   string         `json:"scenario,omitempty"`
	Config     *poker.Config  `json:"config,omitempty"`
	TurnTime   time.Duration  `json:"turnTime,omitempty"`
	Deadline   time.Time      `json:"deadline"`
	Command    *command       `json:"command,omitempty"`
	Deck       *poker.Deck    `json:"deck,omitempty"`
	Escrow     *escrowSession `json:"escrow,omitempty"`
	Binding    string         `json:"binding,omitempty"`
}

type chipMove struct {
	kind   string
	seat   int
	amount poker.Chips
}

func credential(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func initialState(config poker.Config, turnTime time.Duration) (*roomState, error) {
	table, err := poker.NewTable(config)
	if err != nil {
		return nil, err
	}
	return &roomState{
		table: table, turnTime: turnTime,
		players: make([]player, config.Seats), subscribers: make(map[chan snapshot]int),
	}, nil
}

func applyEvent(table *poker.Table, event roomEvent) error {
	switch event.Kind {
	case "create", "join":
		return table.BuyIn(event.Seat)
	case "deal":
		if event.Deck == nil {
			return errors.New("deal event is missing its deck")
		}
		return table.Deal(*event.Deck)
	case "act":
		if event.Command == nil {
			return errors.New("action event is missing its command")
		}
		return table.Act(event.Seat, poker.Action{Kind: event.Command.Action, Target: event.Command.Target})
	case "rebuy":
		return table.Rebuy(event.Seat)
	case "timeout":
		return table.Timeout(event.Seat)
	case "bind-wallet", "sign-checkpoint", "save-checkpoint":
		return nil
	default:
		return errors.New("unknown room event")
	}
}

func (s *roomState) commit(event roomEvent) error {
	if s.storagePaused {
		return errStoragePaused
	}
	candidate := s.table.Clone()
	if err := applyEvent(candidate, event); err != nil {
		return err
	}
	after := candidate.View(poker.NoSeat)
	event.Deadline = s.deadline
	switch event.Kind {
	case "act", "deal", "timeout":
		event.Deadline = time.Time{}
		if after.Actor != poker.NoSeat {
			event.Deadline = time.Now().Add(s.turnTime).UTC()
		}
	}
	if s.store != nil {
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		moves := chipMoves(s.table.View(poker.NoSeat), after, event)
		if err := s.store.append(ctx, s.id, s.version+1, event, moves); err != nil {
			// A failed commit can have an unknown outcome. Reload the durable
			// history on restart before accepting another move for this room.
			log.Printf("Poker Lab room %s paused after a storage error", s.id)
			s.storagePaused = true
			s.broadcast()
			return errStoragePaused
		}
	}
	s.table = candidate
	s.accept(event)
	s.broadcast()
	return nil
}

func (s *roomState) accept(event roomEvent) {
	s.version++
	s.deadline = event.Deadline
	s.acceptEscrow(event)
	if event.Kind == "create" {
		s.scenario = event.Scenario
	}
	if event.Credential != "" {
		s.players[event.Seat].credential = event.Credential
	}
	if event.Command != nil {
		p := &s.players[event.Seat]
		if len(p.receipts) == receiptLimit {
			copy(p.receipts, p.receipts[1:])
			p.receipts = p.receipts[:receiptLimit-1]
		}
		p.receipts = append(p.receipts, receipt{command: *event.Command, version: s.version})
	}
}

func chipMoves(before, after poker.View, event roomEvent) []chipMove {
	switch event.Kind {
	case "create", "join":
		return []chipMove{{"buy_in", event.Seat, after.Config.BuyIn}}
	case "rebuy":
		return []chipMove{{"rebuy", event.Seat, after.Config.BuyIn}}
	}
	var moves []chipMove
	for i, seat := range after.Seats {
		wager := seat.Total
		if before.Hand == after.Hand {
			wager -= before.Seats[i].Total
		}
		if wager > 0 {
			moves = append(moves, chipMove{"wager", i, wager})
		}
	}
	if after.Result != nil && (before.Result == nil || before.Hand != after.Hand) {
		for i, award := range after.Result.Awards {
			if award > 0 {
				moves = append(moves, chipMove{"award", i, award})
			}
			if refund := after.Result.Refunds[i]; refund > 0 {
				moves = append(moves, chipMove{"refund", i, refund})
			}
		}
	}
	return moves
}
