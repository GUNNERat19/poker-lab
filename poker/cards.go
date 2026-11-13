package poker

import (
	"crypto/rand"
	"math/big"
)

type Suit uint8

const (
	Clubs Suit = iota
	Diamonds
	Hearts
	Spades
)

// Card encodes ranks 2 through 14 (ace) in each suit.
type Card uint8

func (c Card) Rank() int  { return int(c)%13 + 2 }
func (c Card) Suit() Suit { return Suit(c / 13) }

type Deck [52]Card

func OrderedDeck() Deck {
	var deck Deck
	for i := range deck {
		deck[i] = Card(i)
	}
	return deck
}

// ShuffleDeck uses an unbiased Fisher–Yates shuffle with cryptographic randomness.
func ShuffleDeck() (Deck, error) {
	deck := OrderedDeck()
	for i := len(deck) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return Deck{}, err
		}
		deck[i], deck[j.Int64()] = deck[j.Int64()], deck[i]
	}
	return deck, nil
}

func (deck Deck) valid() bool {
	var seen [52]bool
	for _, card := range deck {
		if int(card) >= len(seen) || seen[card] {
			return false
		}
		seen[card] = true
	}
	return true
}

// bestHand compares each five-card combination of the available cards.
func bestHand(cards []Card) uint32 {
	var best uint32
	for a := 0; a < len(cards)-4; a++ {
		for b := a + 1; b < len(cards)-3; b++ {
			for c := b + 1; c < len(cards)-2; c++ {
				for d := c + 1; d < len(cards)-1; d++ {
					for e := d + 1; e < len(cards); e++ {
						five := [5]Card{cards[a], cards[b], cards[c], cards[d], cards[e]}
						best = max(best, rankFive(five))
					}
				}
			}
		}
	}
	return best
}

var rankNames = [...]string{"", "", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten", "Jack", "Queen", "King", "Ace"}
var rankPlurals = [...]string{"", "", "Twos", "Threes", "Fours", "Fives", "Sixes", "Sevens", "Eights", "Nines", "Tens", "Jacks", "Queens", "Kings", "Aces"}

// DescribeHand names the strongest hand using only the given hole cards and
// board. Before the flop it describes the player's starting cards.
func DescribeHand(hole [2]Card, board []Card) string {
	if len(board) == 0 {
		if hole[0].Rank() == hole[1].Rank() {
			return "Pocket " + rankPlurals[hole[0].Rank()]
		}
		return rankNames[max(hole[0].Rank(), hole[1].Rank())] + "-high"
	}
	if len(board) < 3 || len(board) > 5 {
		return ""
	}
	cards := [7]Card{hole[0], hole[1]}
	copy(cards[2:], board)
	value := bestHand(cards[:2+len(board)])
	first, second := (value>>16)&15, (value>>12)&15
	high := rankNames[first] + "-high"
	switch value >> 20 {
	case 8:
		if first == 14 {
			return "Royal flush"
		}
		return high + " straight flush"
	case 7:
		return "Four of a kind, " + rankPlurals[first]
	case 6:
		return "Full house, " + rankPlurals[first] + " over " + rankPlurals[second]
	case 5:
		return high + " flush"
	case 4:
		return high + " straight"
	case 3:
		return "Three of a kind, " + rankPlurals[first]
	case 2:
		return "Two pair, " + rankPlurals[first] + " and " + rankPlurals[second]
	case 1:
		return "Pair of " + rankPlurals[first]
	default:
		return high
	}
}

// Scores order hands by category first, then by relevant ranks high to low.
func rankFive(cards [5]Card) uint32 {
	var counts [15]int
	flush := true
	for _, card := range cards {
		counts[card.Rank()]++
		flush = flush && card.Suit() == cards[0].Suit()
	}
	var quads, trips int
	var pairs, singles []int
	for rank := 14; rank >= 2; rank-- {
		switch counts[rank] {
		case 4:
			quads = rank
		case 3:
			trips = rank
		case 2:
			pairs = append(pairs, rank)
		case 1:
			singles = append(singles, rank)
		}
	}
	straight := 0
	if len(singles) == 5 {
		if singles[0]-singles[4] == 4 {
			straight = singles[0]
		} else if singles[0] == 14 && singles[1] == 5 && singles[4] == 2 {
			straight = 5
		}
	}
	switch {
	case flush && straight != 0:
		return score(8, straight)
	case quads != 0:
		return score(7, quads, singles[0])
	case trips != 0 && len(pairs) == 1:
		return score(6, trips, pairs[0])
	case flush:
		return score(5, singles...)
	case straight != 0:
		return score(4, straight)
	case trips != 0:
		return score(3, trips, singles[0], singles[1])
	case len(pairs) == 2:
		return score(2, pairs[0], pairs[1], singles[0])
	case len(pairs) == 1:
		return score(1, pairs[0], singles[0], singles[1], singles[2])
	default:
		return score(0, singles...)
	}
}

func score(category uint32, ranks ...int) uint32 {
	value := category << 20
	for i, rank := range ranks {
		value |= uint32(rank) << (4 * (4 - i))
	}
	return value
}
