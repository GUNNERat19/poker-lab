package demo

import (
	"fmt"

	"cappedpoker/poker"
)

type scenario struct {
	id, name, description string
	config                poker.Config
	firstDeck             func() (poker.Deck, error)
}

var scenarios = []scenario{
	{
		id: "cap", name: "Raise cap",
		description: "Four seats and a fixed deck. Open the flop for 20, raise to 80, then raise to 160 to hit the ceiling.",
		config:      poker.Config{Seats: 4, SmallBlind: 2, BigBlind: 5, BuyIn: 500},
		firstDeck: func() (poker.Deck, error) {
			return riggedDeck(map[int]poker.Card{
				0: card(12, poker.Clubs), 1: card(13, poker.Clubs), 2: card(14, poker.Clubs), 3: card(11, poker.Clubs),
				4: card(12, poker.Diamonds), 5: card(13, poker.Diamonds), 6: card(14, poker.Diamonds), 7: card(11, poker.Diamonds),
				9: card(2, poker.Hearts), 10: card(4, poker.Spades), 11: card(6, poker.Clubs),
				13: card(8, poker.Diamonds), 15: card(10, poker.Hearts),
			}), nil
		},
	},
	{
		id: "rebuy", name: "Bust and rebuy",
		description: "Two seats start with five chips each. Call three from Seat 1, watch the automatic showdown, then rebuy.",
		config:      poker.Config{Seats: 2, SmallBlind: 2, BigBlind: 5, BuyIn: 5},
		firstDeck: func() (poker.Deck, error) {
			return riggedDeck(map[int]poker.Card{
				0: card(14, poker.Clubs), 1: card(13, poker.Clubs), 2: card(14, poker.Diamonds), 3: card(13, poker.Diamonds),
				5: card(2, poker.Hearts), 6: card(4, poker.Spades), 7: card(6, poker.Clubs),
				9: card(8, poker.Diamonds), 11: card(10, poker.Hearts),
			}), nil
		},
	},
	{
		id: "shuffle", name: "Shuffled table",
		description: "Four seats, 500 chips each, and a fresh shuffle. Play a hand and see how the cap changes from street to street.",
		config:      poker.Config{Seats: 4, SmallBlind: 2, BigBlind: 5, BuyIn: 500},
		firstDeck:   poker.ShuffleDeck,
	},
}

func start(scenarioID string) (scenario, *poker.Table, error) {
	for _, choice := range scenarios {
		if choice.id != scenarioID {
			continue
		}
		table, err := poker.NewTable(choice.config)
		if err != nil {
			return scenario{}, nil, err
		}
		for seat := range choice.config.Seats {
			if err := table.BuyIn(seat); err != nil {
				return scenario{}, nil, err
			}
		}
		deck, err := choice.firstDeck()
		if err != nil {
			return scenario{}, nil, err
		}
		return choice, table, table.Deal(deck)
	}
	return scenario{}, nil, fmt.Errorf("choose a practice scenario")
}

func card(rank int, suit poker.Suit) poker.Card {
	return poker.Card(int(suit)*13 + rank - 2)
}

// riggedDeck sets the practice cards and fills the rest with unused cards.
func riggedDeck(fixed map[int]poker.Card) poker.Deck {
	var deck poker.Deck
	var used [52]bool
	for _, card := range fixed {
		used[card] = true
	}
	var next poker.Card
	for i := range deck {
		if card, ok := fixed[i]; ok {
			deck[i] = card
			continue
		}
		for used[next] {
			next++
		}
		deck[i] = next
		next++
	}
	return deck
}
