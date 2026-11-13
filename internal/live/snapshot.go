package live

import (
	"slices"
	"time"

	"cappedpoker/poker"
)

type ack struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Error   string `json:"error,omitempty"`
}

type snapshot struct {
	Type          string        `json:"type"`
	Version       uint64        `json:"version"`
	Hand          uint64        `json:"hand"`
	Street        poker.Street  `json:"street"`
	Viewer        int           `json:"viewer"`
	Actor         int           `json:"actor"`
	Button        int           `json:"button"`
	Small         int           `json:"small"`
	Big           int           `json:"big"`
	Current       poker.Chips   `json:"current"`
	Ceiling       poker.Chips   `json:"ceiling"`
	Pot           poker.Chips   `json:"pot"`
	BuyIn         poker.Chips   `json:"buyIn"`
	SmallBlind    poker.Chips   `json:"smallBlind"`
	BigBlind      poker.Chips   `json:"bigBlind"`
	Scenario      string        `json:"scenario,omitempty"`
	Deadline      *time.Time    `json:"deadline,omitempty"`
	CanDeal       bool          `json:"canDeal"`
	CanRebuy      bool          `json:"canRebuy"`
	CanJoin       bool          `json:"canJoin"`
	Board         []string      `json:"board"`
	BestHand      string        `json:"bestHand,omitempty"`
	Seats         []seat        `json:"seats"`
	Actions       []action      `json:"actions"`
	Pots          []pot         `json:"pots,omitempty"`
	Refunds       []poker.Chips `json:"refunds,omitempty"`
	StoragePaused bool          `json:"storagePaused,omitempty"`
	Escrow        *escrowView   `json:"escrow,omitempty"`
}

type seat struct {
	Occupied bool        `json:"occupied"`
	Stack    poker.Chips `json:"stack"`
	Wager    poker.Chips `json:"wager"`
	AllIn    bool        `json:"allIn"`
	Folded   bool        `json:"folded"`
	InHand   bool        `json:"inHand"`
	Cards    []string    `json:"cards,omitempty"`
}

type action struct {
	Kind   poker.ActionKind `json:"kind"`
	Target poker.Chips      `json:"target"`
	Add    poker.Chips      `json:"add"`
	To     poker.Chips      `json:"to"`
	AllIn  bool             `json:"allIn"`
}

type pot struct {
	Amount  poker.Chips `json:"amount"`
	Winners []int       `json:"winners"`
}

func (s *roomState) snapshot(viewer int) snapshot {
	v := s.table.View(viewer)
	state := snapshot{
		Type: "state", Version: s.version, Hand: v.Hand, Street: v.Street, Viewer: viewer,
		Actor: v.Actor, Button: v.Button, Small: v.Small, Big: v.Big, Current: v.Current,
		Ceiling: v.Ceiling, BuyIn: v.Config.BuyIn, Board: cardCodes(v.Board),
		SmallBlind: v.Config.SmallBlind, BigBlind: v.Config.BigBlind, Scenario: s.scenario,
		Seats: make([]seat, len(v.Seats)), Actions: make([]action, 0, len(v.Actions)),
		StoragePaused: s.storagePaused,
	}
	if !s.deadline.IsZero() {
		deadline := s.deadline
		state.Deadline = &deadline
	}
	betweenHands := v.Street == poker.Waiting || v.Street == poker.Settled
	for i, player := range v.Seats {
		state.Seats[i] = seat{
			Occupied: player.Occupied, Stack: player.Stack, Wager: player.Street,
			InHand: player.InHand, AllIn: player.AllIn, Folded: player.Folded, Cards: cardCodes(player.Cards),
		}
		if i == viewer && !player.Folded && len(player.Cards) == 2 {
			state.BestHand = poker.DescribeHand([2]poker.Card(player.Cards), v.Board)
		}
		state.Pot += player.Total
		if !player.Occupied && betweenHands && viewer == poker.NoSeat {
			state.CanJoin = true
		}
	}
	state.CanDeal = betweenHands && viewer == 0 && s.enoughToDeal(v) && s.escrowReady()
	state.CanRebuy = s.escrow == nil && betweenHands && viewer != poker.NoSeat && v.Seats[viewer].Stack == 0
	if s.escrow != nil {
		state.Escrow = &escrowView{escrowSession: *s.escrow, RoomID: hashText(s.id),
			Bound: slices.Clone(s.bound), Saved: slices.Clone(s.saved), Connected: s.escrowSource != nil}
		if viewer != poker.NoSeat {
			state.Escrow.Pending, state.Escrow.Latest = copyCheckpoint(s.pending), copyCheckpoint(s.latest)
		}
	}
	for _, option := range v.Actions {
		state.Actions = append(state.Actions, action{
			Kind: option.Action.Kind, Target: option.Action.Target,
			Add: option.Add, To: option.To, AllIn: option.AllIn,
		})
	}
	if v.Result != nil {
		state.Pot, state.Current, state.Ceiling = 0, 0, 0
		for _, p := range v.Result.Pots {
			state.Pot += p.Amount
			state.Pots = append(state.Pots, pot{Amount: p.Amount, Winners: p.Winners})
		}
		state.Refunds = v.Result.Refunds
	}
	return state
}

func cardCodes(cards []poker.Card) []string {
	result := make([]string, 0, len(cards))
	for _, card := range cards {
		result = append(result, string([]byte{"23456789TJQKA"[card.Rank()-2], "cdhs"[card.Suit()]}))
	}
	return result
}
