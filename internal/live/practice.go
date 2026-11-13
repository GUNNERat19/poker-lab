package live

import (
	"slices"

	"cappedpoker/poker"
)

const sidePotPractice = "side-pot"

func (s *roomState) enoughToDeal(v poker.View) bool {
	minimum := 2
	if s.scenario == sidePotPractice && v.Hand < 2 {
		minimum = 3
	}
	funded := 0
	for _, seat := range v.Seats {
		if seat.Stack > 0 {
			funded++
		}
	}
	return funded >= minimum
}

func (s *roomState) nextDeck(previousHand uint64) (poker.Deck, error) {
	if s.scenario != sidePotPractice || previousHand >= 2 {
		return poker.ShuffleDeck()
	}
	card := func(rank int, suit poker.Suit) poker.Card {
		return poker.Card(int(suit)*13 + rank - 2)
	}
	deck := poker.OrderedDeck()
	var fixed map[int]poker.Card
	if previousHand == 0 {
		// A royal flush on the board splits the opening pot between the live seats.
		fixed = map[int]poker.Card{
			7: card(10, poker.Spades), 8: card(11, poker.Spades), 9: card(12, poker.Spades),
			11: card(13, poker.Spades), 13: card(14, poker.Spades),
		}
	} else {
		// Dealt clockwise from Seat 2: Seat 3 kings, Seat 1 queens, Seat 2 aces.
		fixed = map[int]poker.Card{
			0: card(13, poker.Clubs), 1: card(12, poker.Clubs), 2: card(14, poker.Clubs),
			3: card(13, poker.Diamonds), 4: card(12, poker.Diamonds), 5: card(14, poker.Diamonds),
			7: card(2, poker.Hearts), 8: card(4, poker.Spades), 9: card(6, poker.Clubs),
			11: card(8, poker.Diamonds), 13: card(10, poker.Hearts),
		}
	}
	// Set the selected positions in order so the entire deck is repeatable.
	for position := range deck {
		if want, ok := fixed[position]; ok {
			current := slices.Index(deck[:], want)
			deck[position], deck[current] = deck[current], deck[position]
		}
	}
	return deck, nil
}
