package poker

import "slices"

type Pot struct {
	Amount   Chips
	Eligible []int
	Winners  []int // In order of priority for odd chips.
}

type Result struct {
	Showdown bool
	Pots     []Pot
	Awards   []Chips // Indexed by seat.
	Refunds  []Chips // Uncalled wagers, indexed by seat.
}

func (t *Table) settle(showdown bool) {
	h := t.hand
	result := &Result{
		Showdown: showdown,
		Awards:   make([]Chips, len(h.players)),
		Refunds:  make([]Chips, len(h.players)),
	}
	var levels []Chips
	var ranks [6]uint32
	for i, player := range h.players {
		if player.total > 0 {
			levels = append(levels, player.total)
		}
		if showdown && player.live() {
			cards := [7]Card{player.hole[0], player.hole[1]}
			copy(cards[2:], h.board)
			ranks[i] = bestHand(cards[:])
		}
	}
	slices.Sort(levels)

	var previous Chips
	for _, level := range slices.Compact(levels) {
		var contributors int
		lastContributor := NoSeat
		var eligible []int
		for i := 1; i <= len(h.players); i++ {
			position := (h.button + i) % len(h.players)
			player := h.players[position]
			if player.total < level {
				continue
			}
			contributors++
			lastContributor = position
			if player.live() {
				eligible = append(eligible, position)
			}
		}
		amount := (level - previous) * Chips(contributors)
		previous = level
		if contributors == 1 {
			result.Refunds[lastContributor] += amount
			continue
		}
		if last := len(result.Pots) - 1; last >= 0 && slices.Equal(result.Pots[last].Eligible, eligible) {
			result.Pots[last].Amount += amount
		} else {
			result.Pots = append(result.Pots, Pot{Amount: amount, Eligible: eligible})
		}
	}
	for i := range result.Pots {
		pot := &result.Pots[i]
		var highest uint32
		for _, position := range pot.Eligible {
			if rank := ranks[position]; rank >= highest {
				if rank > highest {
					highest, pot.Winners = rank, pot.Winners[:0]
				}
				pot.Winners = append(pot.Winners, position)
			}
		}
		count := Chips(len(pot.Winners))
		share, remainder := pot.Amount/count, pot.Amount%count
		for i, winner := range pot.Winners {
			result.Awards[winner] += share
			if Chips(i) < remainder {
				result.Awards[winner]++
			}
		}
	}
	for i := range t.seats {
		t.seats[i].stack += result.Awards[i] + result.Refunds[i]
	}
	h.street, h.actor, h.result = Settled, NoSeat, result
}

type SeatView struct {
	Occupied bool
	Stack    Chips
	InHand   bool
	Folded   bool
	AllIn    bool
	Street   Chips
	Total    Chips
	Cards    []Card
}

type View struct {
	Config                    Config
	Hand                      uint64
	Street                    Street
	Button, Small, Big, Actor int
	Current, Ceiling          Chips
	Board                     []Card
	Seats                     []SeatView
	Actions                   []Option
	Result                    *Result
}

// View returns a detached snapshot. The caller authenticates the viewer's seat.
// Use NoSeat for an observer.
func (t *Table) View(viewer int) View {
	view := View{
		Config: t.config, Street: Waiting, Button: NoSeat, Small: NoSeat, Big: NoSeat, Actor: NoSeat,
		Seats: make([]SeatView, len(t.seats)),
	}
	h := t.hand
	for i, seat := range t.seats {
		snapshot := &view.Seats[i]
		snapshot.Occupied, snapshot.Stack = seat.occupied, seat.stack
		if h == nil {
			continue
		}
		player := h.players[i]
		snapshot.InHand, snapshot.Folded = player.dealt, player.folded
		snapshot.AllIn = t.active() && player.live() && seat.stack == 0
		snapshot.Street, snapshot.Total = player.street, player.total
		if player.dealt && (viewer == i || (h.result != nil && h.result.Showdown && player.live())) {
			snapshot.Cards = slices.Clone(player.hole[:])
		}
	}
	if h != nil {
		view.Hand, view.Street, view.Board = h.number, h.street, slices.Clone(h.board)
		view.Button, view.Small, view.Big, view.Actor = h.button, h.small, h.big, h.actor
		view.Current, view.Ceiling = h.current, h.ceiling
		view.Actions = t.LegalActions(viewer)
		if h.result != nil {
			result := *h.result
			result.Awards, result.Refunds = slices.Clone(result.Awards), slices.Clone(result.Refunds)
			result.Pots = slices.Clone(result.Pots)
			for i := range result.Pots {
				result.Pots[i].Eligible = slices.Clone(result.Pots[i].Eligible)
				result.Pots[i].Winners = slices.Clone(result.Pots[i].Winners)
			}
			view.Result = &result
		}
	}
	return view
}
