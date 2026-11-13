package poker

type ActionKind string

const (
	Check ActionKind = "check"
	Fold  ActionKind = "fold"
	Call  ActionKind = "call"
	Bet   ActionKind = "bet"
	Raise ActionKind = "raise"
	AllIn ActionKind = "all-in"
)

type Action struct {
	Kind   ActionKind
	Target Chips // Street total for Bet and Raise. Zero for the other actions.
}

type Option struct {
	Action Action
	Add    Chips
	To     Chips
	AllIn  bool
}

func (t *Table) LegalActions(position int) []Option {
	if !t.active() || position == NoSeat || position != t.hand.actor {
		return nil
	}
	h := t.hand
	player := h.players[position]
	stack := t.seats[position].stack
	available := player.street + stack
	option := func(action Action, to Chips) Option {
		add := to - player.street
		return Option{Action: action, Add: add, To: to, AllIn: add == stack}
	}

	var options []Option
	if player.street < h.current {
		options = append(options, option(Action{Kind: Fold}, player.street))
		options = append(options, option(Action{Kind: Call}, min(h.current, available)))
	} else {
		options = append(options, option(Action{Kind: Check}, player.street))
	}
	if player.acted && h.lastFullRaise > 0 && h.current-player.actedAt < h.lastFullRaise {
		return options
	}
	opponent := t.nextSeat(position, func(other int) bool {
		return other != position && h.players[other].live() && t.seats[other].stack > 0
	})
	if opponent == NoSeat {
		return options
	}

	kind := Raise
	if h.current == 0 {
		kind = Bet
	}
	base, ceiling, first := h.current, h.ceiling, Chips(2)
	if h.lastFullRaise == 0 {
		base, first = t.config.BigBlind, 1
		if ceiling == 0 {
			ceiling = 8 * base
		}
	}
	minimumIncrement := h.lastFullRaise
	for multiplier := first; multiplier <= 8; multiplier *= 2 {
		if base > min(ceiling, available)/multiplier {
			break
		}
		target := base * multiplier
		if target-h.current >= minimumIncrement {
			options = append(options, option(Action{Kind: kind, Target: target}, target))
		}
	}
	short := available-h.current < minimumIncrement
	if h.lastFullRaise == 0 {
		short = available < t.config.BigBlind
	}
	if short && available > h.current && available <= ceiling {
		options = append(options, option(Action{Kind: AllIn}, available))
	}
	return options
}

func (t *Table) Act(position int, action Action) error {
	for _, option := range t.LegalActions(position) {
		if option.Action == action || (action == (Action{Kind: AllIn}) && option.AllIn) {
			t.apply(position, option)
			return nil
		}
	}
	return ErrIllegalMove
}

// Timeout checks if possible and folds when the player faces a wager.
func (t *Table) Timeout(position int) error {
	for _, option := range t.LegalActions(position) {
		switch option.Action.Kind {
		case Check, Fold:
			t.apply(position, option)
			return nil
		}
	}
	return ErrIllegalMove
}

func (t *Table) apply(position int, option Option) {
	h := t.hand
	player := &h.players[position]
	if option.Action.Kind == Fold {
		player.folded = true
	}
	if option.Add > 0 {
		t.commit(position, option.Add)
	}
	if option.To > h.current {
		if h.ceiling == 0 {
			h.ceiling = 8 * max(t.config.BigBlind, option.To)
		}
		if h.lastFullRaise == 0 {
			if option.To >= t.config.BigBlind {
				h.lastFullRaise = option.To
				// A full opening bet reopens action after an incomplete opener.
				for i := range h.players {
					h.players[i].acted = false
				}
			}
		} else if increment := option.To - h.current; increment >= h.lastFullRaise {
			h.lastFullRaise = increment
		}
		h.current = option.To
	}
	player.acted, player.actedAt = true, h.current
	t.advance(position)
}
