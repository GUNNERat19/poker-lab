package poker

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

func fundedTable(t testing.TB, stacks ...Chips) *Table {
	t.Helper()
	config := DefaultConfig()
	config.Seats = len(stacks)
	table, err := NewTable(config)
	if err != nil {
		t.Fatal(err)
	}
	for i, stack := range stacks {
		if err := table.BuyIn(i); err != nil {
			t.Fatal(err)
		}
		table.seats[i].stack = stack
	}
	return table
}

func deal(t testing.TB, table *Table, deck Deck) {
	t.Helper()
	if err := table.Deal(deck); err != nil {
		t.Fatal(err)
	}
}

func act(t testing.TB, table *Table, position int, kind ActionKind, target Chips) {
	t.Helper()
	if err := table.Act(position, Action{Kind: kind, Target: target}); err != nil {
		view := table.View(position)
		t.Fatalf("%s: seat %d %s to %d: %v (actor %d, options %v)",
			view.Street, position, kind, target, err, view.Actor, view.Actions)
	}
}

func targets(table *Table, seat int) []Chips {
	var result []Chips
	for _, option := range table.LegalActions(seat) {
		if option.Action.Kind == Raise || option.Action.Kind == Bet {
			result = append(result, option.To)
		}
	}
	return result
}

func TestStreetCeilingDoesNotCompound(t *testing.T) {
	table := fundedTable(t, 500, 500, 500, 500)
	deal(t, table, OrderedDeck())
	for _, position := range []int{3, 0, 1} {
		act(t, table, position, Call, 0)
	}
	act(t, table, 2, Check, 0)
	act(t, table, 1, Bet, 20)
	view := table.View(2)
	if view.Ceiling != 160 || !slices.Equal(targets(table, 2), []Chips{40, 80, 160}) {
		t.Fatalf("unexpected options at 20: %+v", view.Actions)
	}
	act(t, table, 2, Raise, 80)
	if got := targets(table, 3); !slices.Equal(got, []Chips{160}) {
		t.Fatalf("raise targets after 80: %v", got)
	}
	act(t, table, 3, Raise, 160)
	before := table.View(0)
	for _, invalid := range []Action{{Kind: Raise, Target: 320}, {Kind: AllIn}, {Kind: Check}} {
		if err := table.Act(0, invalid); !errors.Is(err, ErrIllegalMove) {
			t.Fatalf("accepted illegal action %+v: %v", invalid, err)
		}
	}
	if !reflect.DeepEqual(table.View(0), before) || len(targets(table, 0)) != 0 {
		t.Fatal("illegal actions changed the table or a raise was offered at the ceiling")
	}
	act(t, table, 0, Fold, 0)
	options := table.LegalActions(1)
	if len(options) != 2 || options[1].Action.Kind != Call || options[1].Add != 140 {
		t.Fatalf("seat 1 already put in 20, options: %+v", options)
	}
	act(t, table, 1, Call, 0)
	act(t, table, 2, Call, 0)
	if view := table.View(1); view.Street != Turn || view.Ceiling != 0 ||
		!slices.Equal(targets(table, 1), []Chips{5, 10, 20, 40}) {
		t.Fatalf("turn should have fresh betting, got %+v", view)
	}
}

func TestCumulativeShortAllInsReopenPerPlayer(t *testing.T) {
	table := fundedTable(t, 500, 10, 500, 500, 6, 8)
	deal(t, table, OrderedDeck())
	act(t, table, 3, Call, 0) // Acted facing 5.
	act(t, table, 4, AllIn, 0)
	act(t, table, 5, AllIn, 0)
	act(t, table, 0, Call, 0) // Acted facing 8.
	act(t, table, 1, AllIn, 0)
	act(t, table, 2, Call, 0)
	if got := targets(table, 3); !slices.Equal(got, []Chips{20, 40}) {
		t.Fatalf("increase of 5 should reopen seat 3, got %v", got)
	}
	act(t, table, 3, Call, 0)
	if got := targets(table, 0); len(got) != 0 {
		t.Fatalf("increase of 2 should not reopen seat 0, got %v", got)
	}
	act(t, table, 0, Call, 0)
}

func TestCompleteShortPostflopOpener(t *testing.T) {
	table := fundedTable(t, 500, 8, 500)
	deal(t, table, OrderedDeck())
	act(t, table, 0, Call, 0)
	act(t, table, 1, Call, 0)
	act(t, table, 2, Check, 0)
	act(t, table, 1, AllIn, 0)
	if view := table.View(2); view.Current != 3 || view.Ceiling != 40 ||
		!slices.Equal(targets(table, 2), []Chips{5, 10, 20, 40}) {
		t.Fatalf("short opener should set a big-blind ceiling and allow completion: %+v", view)
	}
	act(t, table, 2, Call, 0)
	act(t, table, 0, Raise, 5)
	if got := targets(table, 2); !slices.Equal(got, []Chips{10, 20, 40}) {
		t.Fatalf("full opening bet should reopen seat 2: %v", got)
	}
	act(t, table, 2, Raise, 40)
}

func TestShortBigBlind(t *testing.T) {
	t.Run("heads-up call uses actual opposing wager", func(t *testing.T) {
		table := fundedTable(t, 500, 3)
		deal(t, table, OrderedDeck())
		view := table.View(0)
		if view.Actor != 0 || view.Button != 0 || view.Small != 0 || view.Big != 1 ||
			view.Current != 3 || len(view.Actions) != 2 || view.Actions[1].Add != 1 {
			t.Fatalf("expected a one-chip call or fold: %+v", view)
		}
		act(t, table, 0, Call, 0)
		view = table.View(NoSeat)
		if view.Result == nil || !view.Result.Showdown || len(view.Board) != 5 ||
			!slices.Equal(view.Result.Refunds, []Chips{0, 0}) {
			t.Fatalf("expected automatic runout with matched bets: %+v", view)
		}
	})
	t.Run("multiway keeps the nominal target", func(t *testing.T) {
		table := fundedTable(t, 500, 500, 3)
		deal(t, table, OrderedDeck())
		view := table.View(0)
		if view.Actor != 0 || view.Current != 5 || !slices.Equal(targets(table, 0), []Chips{10, 20, 40}) {
			t.Fatalf("nominal big blind should set the opening wager: %+v", view)
		}
	})
}

func rigDeck(cards map[int]Card) Deck {
	var deck Deck
	var used [52]bool
	for i, card := range cards {
		deck[i], used[card] = card, true
	}
	var next Card
	for i := range deck {
		if _, set := cards[i]; !set {
			for used[next] {
				next++
			}
			deck[i] = next
			next++
		}
	}
	return deck
}

func card(rank int, suit Suit) Card {
	return Card(13*int(suit) + rank - 2)
}

func TestSidePotsRefundsAndShowdownPrivacy(t *testing.T) {
	table := fundedTable(t, 500, 20, 500, 10)
	deal(t, table, rigDeck(map[int]Card{
		0: card(12, Clubs), 1: card(13, Clubs), 2: card(14, Clubs), 3: card(11, Clubs),
		4: card(12, Diamonds), 5: card(13, Diamonds), 6: card(14, Diamonds), 7: card(11, Diamonds),
		9: card(2, Hearts), 10: card(4, Spades), 11: card(6, Clubs), 13: card(8, Diamonds), 15: card(10, Hearts),
	}))
	private := table.View(3)
	for i, player := range private.Seats {
		if (len(player.Cards) == 2) != (i == 3) {
			t.Fatalf("seat 3 received wrong cards for seat %d", i)
		}
	}
	for i, player := range table.View(NoSeat).Seats {
		if len(player.Cards) != 0 {
			t.Fatalf("observer saw cards for seat %d", i)
		}
	}
	act(t, table, 3, AllIn, 0)
	act(t, table, 0, Raise, 40)
	act(t, table, 1, Call, 0)
	act(t, table, 2, Fold, 0)
	view := table.View(NoSeat)
	want := []Pot{
		{Amount: 35, Eligible: []int{1, 3, 0}, Winners: []int{3}},
		{Amount: 20, Eligible: []int{1, 0}, Winners: []int{1}},
	}
	if view.Result == nil || !view.Result.Showdown || !reflect.DeepEqual(view.Result.Pots, want) ||
		!slices.Equal(view.Result.Refunds, []Chips{20, 0, 0, 0}) ||
		!slices.Equal(view.Result.Awards, []Chips{0, 20, 0, 35}) {
		t.Fatalf("unexpected settlement: %+v", view.Result)
	}
	if len(view.Seats[2].Cards) != 0 || len(view.Seats[3].Cards) != 2 {
		t.Fatalf("wrong cards revealed at showdown: %+v", view.Seats)
	}
	view.Seats[3].Cards[0] = 0
	view.Board[0] = 0
	view.Result.Pots[0].Winners[0] = 2
	again := table.View(NoSeat)
	if !reflect.DeepEqual(again.Result.Pots, want) || again.Seats[3].Cards[0] != card(14, Clubs) ||
		again.Board[0] != card(2, Hearts) {
		t.Fatal("a view must not alias table state")
	}
}

func TestTieAndOddChip(t *testing.T) {
	table := fundedTable(t, 500, 500, 500)
	deal(t, table, rigDeck(map[int]Card{
		7: card(10, Hearts), 8: card(11, Hearts), 9: card(12, Hearts), 11: card(13, Hearts), 13: card(14, Hearts),
	}))
	act(t, table, 0, Call, 0)
	act(t, table, 1, Call, 0)
	act(t, table, 2, Check, 0)
	act(t, table, 1, Bet, 5)
	act(t, table, 2, Call, 0)
	act(t, table, 0, Fold, 0)
	for range 2 {
		act(t, table, 1, Check, 0)
		act(t, table, 2, Check, 0)
	}
	result := table.View(NoSeat).Result
	if result == nil || !slices.Equal(result.Awards, []Chips{0, 13, 12}) {
		t.Fatalf("expected to split a 25-chip pot with the odd chip to seat 1, got %+v", result)
	}
}

func TestRebuyAndButtonRotation(t *testing.T) {
	config := Config{Seats: 2, SmallBlind: 2, BigBlind: 5, BuyIn: 5}
	table, err := NewTable(config)
	if err != nil {
		t.Fatal(err)
	}
	for position := range 2 {
		if err := table.BuyIn(position); err != nil {
			t.Fatal(err)
		}
	}
	deck := rigDeck(map[int]Card{
		0: card(14, Clubs), 1: card(13, Clubs), 2: card(14, Diamonds), 3: card(13, Diamonds),
		5: card(2, Hearts), 6: card(4, Spades), 7: card(6, Clubs), 9: card(8, Diamonds), 11: card(10, Hearts),
	})
	deal(t, table, deck)
	if err := table.Rebuy(1); !errors.Is(err, ErrHandActive) {
		t.Fatalf("expected active-hand rebuy to fail, got %v", err)
	}
	act(t, table, 0, Call, 0)
	if err := table.Deal(deck); !errors.Is(err, ErrPlayers) {
		t.Fatalf("expected only one funded seat, got %v", err)
	}
	if err := table.Rebuy(1); !errors.Is(err, ErrSeat) {
		t.Fatalf("expected funded-seat rebuy to fail, got %v", err)
	}
	if err := table.Rebuy(0); err != nil {
		t.Fatal(err)
	}
	if err := table.Rebuy(0); !errors.Is(err, ErrSeat) {
		t.Fatalf("expected repeated rebuy to fail, got %v", err)
	}
	if view := table.View(NoSeat); view.Seats[0].Stack != 5 || view.Seats[1].Stack != 10 {
		t.Fatalf("expected rebuy to restore exactly five chips, got %+v", view.Seats)
	}
	deal(t, table, deck)
	if view := table.View(1); view.Hand != 2 || view.Button != 1 || view.Small != 1 || view.Big != 0 || view.Actor != 1 {
		t.Fatalf("unexpected button rotation: %+v", view)
	}
}

func TestTimeoutAndInvalidDeck(t *testing.T) {
	table := fundedTable(t, 500, 500)
	if err := table.Deal(Deck{}); !errors.Is(err, ErrDeck) || table.View(NoSeat).Street != Waiting {
		t.Fatalf("invalid deck changed the table: %v", err)
	}
	deal(t, table, OrderedDeck())
	if err := table.Timeout(1); !errors.Is(err, ErrIllegalMove) {
		t.Fatalf("out-of-turn timeout: %v", err)
	}
	act(t, table, 0, Call, 0)
	if err := table.Timeout(1); err != nil {
		t.Fatal(err)
	}
	if view := table.View(1); view.Street != Flop || view.Actor != 1 {
		t.Fatalf("expected timeout check with BB acting first on flop: %+v", view)
	}
	act(t, table, 1, Bet, 5)
	if err := table.Timeout(0); err != nil {
		t.Fatal(err)
	}
	view := table.View(NoSeat)
	if view.Result == nil || view.Result.Showdown || view.Result.Refunds[1] != 5 ||
		len(view.Seats[1].Cards) != 0 {
		t.Fatalf("expected timeout fold, no reveal, and an uncalled bet refund: %+v", view)
	}
}

func TestHandRanking(t *testing.T) {
	t.Run("wheel loses to a six-high straight", func(t *testing.T) {
		low := rankFive([5]Card{card(14, Clubs), card(2, Hearts), card(3, Clubs), card(4, Clubs), card(5, Clubs)})
		high := rankFive([5]Card{card(2, Hearts), card(3, Clubs), card(4, Clubs), card(5, Clubs), card(6, Clubs)})
		if low>>20 != 4 || high>>20 != 4 || low >= high {
			t.Fatalf("unexpected straight order: wheel %d, six-high %d", low, high)
		}
	})
	t.Run("best of two trips is the higher full house", func(t *testing.T) {
		aces := [3]Card{card(14, Clubs), card(14, Hearts), card(14, Diamonds)}
		kings := [3]Card{card(13, Clubs), card(13, Hearts), card(13, Diamonds)}
		actual := bestHand([]Card{aces[0], aces[1], aces[2], kings[0], kings[1], kings[2], card(2, Clubs)})
		want := rankFive([5]Card{aces[0], aces[1], aces[2], kings[0], kings[1]})
		if actual != want || actual>>20 != 6 {
			t.Fatalf("expected aces full of kings, got score %d", actual)
		}
	})
	t.Run("top kicker breaks a pair tie", func(t *testing.T) {
		board := [4]Card{card(12, Clubs), card(12, Hearts), card(10, Clubs), card(7, Clubs)}
		king := rankFive([5]Card{board[0], board[1], board[2], board[3], card(13, Clubs)})
		jack := rankFive([5]Card{board[0], board[1], board[2], board[3], card(11, Clubs)})
		if king>>20 != 1 || jack>>20 != 1 || king <= jack {
			t.Fatalf("unexpected kicker order: king %d, jack %d", king, jack)
		}
	})
}

func TestDescribeHandAsBoardDevelops(t *testing.T) {
	hole := [2]Card{card(14, Clubs), card(9, Hearts)}
	board := []Card{card(14, Diamonds), card(7, Clubs), card(2, Spades), card(7, Hearts), card(7, Diamonds)}
	for _, test := range []struct {
		count int
		want  string
	}{
		{0, "Ace-high"},
		{3, "Pair of Aces"},
		{4, "Two pair, Aces and Sevens"},
		{5, "Full house, Sevens over Aces"},
	} {
		if got := DescribeHand(hole, board[:test.count]); got != test.want {
			t.Errorf("with %d board cards: got %q, want %q", test.count, got, test.want)
		}
	}
	pair := [2]Card{card(14, Clubs), card(14, Hearts)}
	if got := DescribeHand(pair, nil); got != "Pocket Aces" {
		t.Errorf("preflop pair: got %q", got)
	}
	royal := []Card{card(10, Spades), card(11, Spades), card(12, Spades), card(13, Spades), card(14, Spades)}
	if got := DescribeHand(hole, royal); got != "Royal flush" {
		t.Errorf("playing the board: got %q", got)
	}
}

func FuzzHandConservesChips(f *testing.F) {
	for _, seed := range []uint64{0, 1, 42, 1907, 807308} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		random := rand.New(rand.NewPCG(seed, seed+1))
		config := DefaultConfig()
		config.Seats = 2 + random.IntN(5)
		config.BuyIn = 5 + Chips(random.IntN(120))
		table, err := NewTable(config)
		if err != nil {
			t.Fatal(err)
		}
		for i := range config.Seats {
			if err := table.BuyIn(i); err != nil {
				t.Fatal(err)
			}
		}
		for range 12 {
			var before Chips
			for i, seat := range table.View(NoSeat).Seats {
				if seat.Stack == 0 {
					if err := table.Rebuy(i); err != nil {
						t.Fatal(err)
					}
					seat.Stack = config.BuyIn
				}
				before += seat.Stack
			}
			deck := OrderedDeck()
			random.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
			deal(t, table, deck)
			var seen [52]bool
			for i := range config.Seats {
				cards := table.View(i).Seats[i].Cards
				if len(cards) != 2 || seen[cards[0]] || seen[cards[1]] || cards[0] == cards[1] {
					t.Fatalf("invalid hole cards for seat %d: %v", i, cards)
				}
				seen[cards[0]], seen[cards[1]] = true, true
			}
			for steps := 0; table.active(); steps++ {
				if steps >= 256 {
					t.Fatal("hand did not terminate")
				}
				actor := table.View(NoSeat).Actor
				options := table.LegalActions(actor)
				if len(options) == 0 {
					t.Fatalf("active hand stalled at seat %d", actor)
				}
				action := options[random.IntN(len(options))].Action
				act(t, table, actor, action.Kind, action.Target)
			}
			view := table.View(NoSeat)
			for _, card := range view.Board {
				if seen[card] {
					t.Fatalf("duplicate board card: %v", card)
				}
				seen[card] = true
			}
			var after Chips
			for _, seat := range view.Seats {
				if seat.Stack < 0 {
					t.Fatal("negative stack")
				}
				after += seat.Stack
			}
			if before != after {
				t.Fatalf("chip total changed from %d to %d", before, after)
			}
		}
	})
}
