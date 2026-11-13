// Package poker implements the Capped Texas Hold’em rules for a single table.
package poker

import (
	"errors"
	"math"
	"slices"
)

const NoSeat = -1
const RulesVersion = "capped-holdem-v1"

var (
	ErrConfig       = errors.New("invalid table configuration")
	ErrSeat         = errors.New("invalid seat or seat status")
	ErrHandActive   = errors.New("hand in progress")
	ErrPlayers      = errors.New("at least two funded seats required")
	ErrDeck         = errors.New("deck must contain each card exactly once")
	ErrIllegalMove  = errors.New("illegal or out-of-turn action")
	ErrChipOverflow = errors.New("table chip limit exceeded")
)

type Chips int64

type Config struct {
	Seats                       int
	SmallBlind, BigBlind, BuyIn Chips
}

func DefaultConfig() Config {
	return Config{Seats: 6, SmallBlind: 2, BigBlind: 5, BuyIn: 500}
}

type Street string

const (
	Waiting Street = "waiting"
	Preflop Street = "preflop"
	Flop    Street = "flop"
	Turn    Street = "turn"
	River   Street = "river"
	Settled Street = "settled"
)

// Table belongs to one caller at a time. A server should serialize its commands.
type Table struct {
	config Config
	seats  []seat
	hand   *hand
}

// Clone copies the mutable table state so a coordinator can stage a command.
func (t *Table) Clone() *Table {
	copyTable := *t
	copyTable.seats = slices.Clone(t.seats)
	if t.hand != nil {
		copyHand := *t.hand
		copyHand.board = slices.Clone(t.hand.board)
		copyHand.players = slices.Clone(t.hand.players)
		// A settlement result is immutable once assigned.
		copyTable.hand = &copyHand
	}
	return &copyTable
}

type seat struct {
	occupied bool
	stack    Chips
}

type hand struct {
	number                    uint64
	street                    Street
	button, small, big, actor int
	deck                      Deck
	nextCard                  int
	board                     []Card
	players                   []handSeat
	current, ceiling          Chips
	lastFullRaise             Chips
	result                    *Result
}

type handSeat struct {
	dealt, folded bool
	hole          [2]Card
	street, total Chips
	acted         bool
	actedAt       Chips
}

func NewTable(config Config) (*Table, error) {
	if config.Seats < 2 || config.Seats > 6 || config.SmallBlind <= 0 ||
		config.SmallBlind >= config.BigBlind || config.BuyIn < config.BigBlind ||
		config.BigBlind > math.MaxInt64/64 {
		return nil, ErrConfig
	}
	return &Table{config: config, seats: make([]seat, config.Seats)}, nil
}

func (t *Table) BuyIn(position int) error { return t.fund(position, false) }
func (t *Table) Rebuy(position int) error { return t.fund(position, true) }

func (t *Table) fund(position int, rebuy bool) error {
	if t.active() {
		return ErrHandActive
	}
	if position < 0 || position >= len(t.seats) {
		return ErrSeat
	}
	s := &t.seats[position]
	if s.occupied != rebuy || s.stack != 0 {
		return ErrSeat
	}
	var total Chips
	for _, player := range t.seats {
		total += player.stack
	}
	if t.config.BuyIn > math.MaxInt64-total {
		return ErrChipOverflow
	}
	*s = seat{occupied: true, stack: t.config.BuyIn}
	return nil
}

// Deal starts the next hand from the supplied deck, in top-to-bottom order.
func (t *Table) Deal(deck Deck) error {
	if t.active() {
		return ErrHandActive
	}
	funded := 0
	for _, s := range t.seats {
		if s.stack > 0 {
			funded++
		}
	}
	if funded < 2 {
		return ErrPlayers
	}
	if !deck.valid() {
		return ErrDeck
	}

	number, previousButton := uint64(1), NoSeat
	if t.hand != nil {
		number, previousButton = t.hand.number+1, t.hand.button
	}
	canPlay := func(position int) bool { return t.seats[position].stack > 0 }
	button := t.nextSeat(previousButton, canPlay)
	if funded == 2 && t.hand != nil && t.nextSeat(button, canPlay) == t.hand.big {
		previousCount := 0
		for _, player := range t.hand.players {
			if player.dealt {
				previousCount++
			}
		}
		if previousCount > 2 {
			button = t.hand.big
		}
	}
	small := button
	if funded > 2 {
		small = t.nextSeat(button, canPlay)
	}
	big := t.nextSeat(small, canPlay)
	h := &hand{
		number: number, street: Preflop, deck: deck, players: make([]handSeat, len(t.seats)),
		button: button, small: small, big: big, actor: NoSeat,
		current: t.config.BigBlind, lastFullRaise: t.config.BigBlind, ceiling: 8 * t.config.BigBlind,
	}
	for position := range t.seats {
		h.players[position].dealt = canPlay(position)
	}
	t.hand = h
	for card := 0; card < 2; card++ {
		position := button
		for i := 0; i < funded; i++ {
			position = t.nextSeat(position, canPlay)
			h.players[position].hole[card] = h.draw()
		}
	}
	t.commit(small, min(t.config.SmallBlind, t.seats[small].stack))
	t.commit(big, min(t.config.BigBlind, t.seats[big].stack))
	t.advance(big)
	return nil
}

func (t *Table) active() bool { return t.hand != nil && t.hand.street != Settled }

func (t *Table) nextSeat(after int, eligible func(int) bool) int {
	for i := 1; i <= len(t.seats); i++ {
		position := (after + i) % len(t.seats)
		if eligible(position) {
			return position
		}
	}
	return NoSeat
}

func (h *hand) draw() Card {
	card := h.deck[h.nextCard]
	h.nextCard++
	return card
}

func (t *Table) commit(position int, amount Chips) {
	player := &t.hand.players[position]
	t.seats[position].stack -= amount
	player.street += amount
	player.total += amount
}

func (p handSeat) live() bool { return p.dealt && !p.folded }

func (t *Table) advance(after int) {
	h := t.hand
	h.actor = NoSeat
	for {
		var live, able int
		var maxLive Chips
		lastAble := NoSeat
		for position, player := range h.players {
			if !player.live() {
				continue
			}
			live++
			maxLive = max(maxLive, player.street)
			if t.seats[position].stack > 0 {
				able++
				lastAble = position
			}
		}
		if live == 1 {
			t.settle(false)
			return
		}
		if able == 1 {
			h.current = maxLive
			if h.players[lastAble].street < maxLive {
				h.actor = lastAble
				return
			}
		}
		if able >= 2 {
			h.actor = t.nextSeat(after, func(position int) bool {
				player := h.players[position]
				return player.live() && t.seats[position].stack > 0 &&
					(!player.acted || player.street < h.current)
			})
			if h.actor != NoSeat {
				return
			}
		}
		if h.street == River {
			t.settle(true)
			return
		}
		h.nextStreet()
		after = h.button
	}
}

func (h *hand) nextStreet() {
	h.draw() // Burn one card before each community deal.
	count := 1
	switch h.street {
	case Preflop:
		h.street, count = Flop, 3
	case Flop:
		h.street = Turn
	case Turn:
		h.street = River
	}
	for i := 0; i < count; i++ {
		h.board = append(h.board, h.draw())
	}
	h.current, h.ceiling, h.lastFullRaise = 0, 0, 0
	for i := range h.players {
		player := &h.players[i]
		player.street, player.actedAt, player.acted = 0, 0, false
	}
}
