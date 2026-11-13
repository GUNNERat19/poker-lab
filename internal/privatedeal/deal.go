package privatedeal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"cappedpoker/poker"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/proof"
	"go.dedis.ch/kyber/v3/shuffle"
)

var ErrAborted = errors.New("deal aborted")

type Step struct {
	Stage    string
	Seat     int
	Position int
}

type Report struct {
	ShuffleCount int
	ProofBytes   int
	HoleCards    [][2]poker.Card
	Board        []poker.Card
	Showdown     map[int][2]poker.Card
	Elapsed      time.Duration
}

type dealer struct {
	round string
	keys  []kyber.Point
	key   kyber.Point
	deck  encryptedDeck
	mixed int
}

func newDealer(round string, keys []publicKey) (*dealer, error) {
	if len(keys) < 2 || len(keys) > 6 {
		return nil, errors.New("private deal needs two to six seats")
	}
	d := &dealer{round: round, keys: make([]kyber.Point, len(keys)), key: suite.Point().Null()}
	for seat, k := range keys {
		if k.point == nil || k.point.Equal(suite.Point().Null()) || len(k.proof) > 512 {
			return nil, errors.New("invalid player key")
		}
		points := map[string]kyber.Point{"G": suite.Point().Base(), "K": k.point}
		if err := proof.HashVerify(suite, label(round, "key", seat, -1, k.point),
			keyProof.Verifier(suite, points), k.proof); err != nil {
			return nil, fmt.Errorf("seat %d key proof: %w", seat+1, err)
		}
		d.keys[seat] = k.point.Clone()
		d.key.Add(d.key, k.point)
	}
	if d.key.Equal(suite.Point().Null()) {
		return nil, errors.New("invalid aggregate key")
	}
	d.deck = canonicalDeck(d.key)
	return d, nil
}

func (d *dealer) acceptMix(seat int, m mix) error {
	if seat != d.mixed || seat >= len(d.keys) || len(m.deck.x) != cards || len(m.deck.y) != cards ||
		len(m.proof) == 0 || len(m.proof) > maxShuffleProof {
		return errors.New("invalid shuffle submission")
	}
	for i := range cards {
		if m.deck.x[i] == nil || m.deck.y[i] == nil || m.deck.x[i].Equal(suite.Point().Null()) {
			return errors.New("invalid encrypted card")
		}
	}
	if err := proof.HashVerify(suite, shuffleLabel(d.round, seat, d.key, d.deck, m.deck),
		shuffle.Verifier(suite, nil, d.key, d.deck.x, d.deck.y, m.deck.x, m.deck.y), m.proof); err != nil {
		return fmt.Errorf("seat %d shuffle proof: %w", seat+1, err)
	}
	d.deck = m.deck
	d.mixed++
	return nil
}

// collect simulates the messages that independent players would send.
func collect(d *dealer, players []player, step Step, recipient int, withhold func(Step) bool) ([]kyber.Point, error) {
	if d.mixed != len(d.keys) {
		return nil, errors.New("every shuffle must verify before dealing")
	}
	points := make([]kyber.Point, 0, len(players))
	for seat, p := range players {
		// For a hole card, its owner computes their share locally. The
		// coordinator only receives the other shares.
		if seat == recipient {
			continue
		}
		step.Seat = seat
		if withhold != nil && withhold(step) {
			return nil, missing(step)
		}
		s, err := makeShare(d.round, seat, step.Position, p, d.keys[seat], d.deck)
		if err != nil {
			return nil, err
		}
		if err := verifyShare(d.round, seat, step.Position, d.keys[seat], d.deck, s); err != nil {
			return nil, fmt.Errorf("seat %d decryption proof: %w", seat+1, err)
		}
		points = append(points, s.point)
	}
	return points, nil
}

func missing(step Step) error {
	return fmt.Errorf("%w: seat %d withheld %s (deck position %d)", ErrAborted, step.Seat+1, step.Stage, step.Position)
}

// Simulate deals one local hand. The harness simulates all players in one
// process. It returns an empty report if a required participant withholds.
func Simulate(seats int, withhold func(Step) bool) (Report, error) {
	if seats < 2 || seats > 6 {
		return Report{}, errors.New("private deal needs two to six seats")
	}
	start := time.Now()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Report{}, err
	}
	round := hex.EncodeToString(nonce[:])
	players, keys := make([]player, seats), make([]publicKey, seats)
	for seat := range players {
		var err error
		players[seat], keys[seat], err = newPlayer(round, seat)
		if err != nil {
			return Report{}, err
		}
	}
	d, err := newDealer(round, keys)
	if err != nil {
		return Report{}, err
	}
	result := Report{HoleCards: make([][2]poker.Card, seats), Showdown: make(map[int][2]poker.Card)}
	for seat := range players {
		if withhold != nil && withhold(Step{"shuffle", seat, -1}) {
			return Report{}, missing(Step{"shuffle", seat, -1})
		}
		m, err := makeMix(round, seat, d.key, d.deck)
		if err != nil {
			return Report{}, err
		}
		if err := d.acceptMix(seat, m); err != nil {
			return Report{}, err
		}
		result.ShuffleCount++
		result.ProofBytes += len(m.proof)
	}
	for pass := range 2 {
		for seat, p := range players {
			pos := pass*seats + (seat+seats-1)%seats // Seat 1 has the button.
			shares, err := collect(d, players, Step{Stage: "hole share", Position: pos}, seat, withhold)
			if err != nil {
				return Report{}, err
			}
			local := suite.Point().Mul(p.key, d.deck.x[pos])
			card, err := decodeCard(d.deck, pos, append(shares, local))
			if err != nil {
				return Report{}, err
			}
			result.HoleCards[seat][pass] = card
		}
	}
	// The flop, turn, and river each follow a burn card.
	first := 2 * seats
	for _, pos := range []int{first + 1, first + 2, first + 3, first + 5, first + 7} {
		shares, err := collect(d, players, Step{Stage: "board share", Position: pos}, -1, withhold)
		if err != nil {
			return Report{}, err
		}
		card, err := decodeCard(d.deck, pos, shares)
		if err != nil {
			return Report{}, err
		}
		result.Board = append(result.Board, card)
	}
	// Simulate a showdown with Seats 1 and 2. Other seats stay folded.
	for seat := range 2 {
		var hand [2]poker.Card
		for pass := range 2 {
			pos := pass*seats + (seat+seats-1)%seats
			shares, err := collect(d, players, Step{Stage: "showdown share", Position: pos}, -1, withhold)
			if err != nil {
				return Report{}, err
			}
			hand[pass], err = decodeCard(d.deck, pos, shares)
			if err != nil {
				return Report{}, err
			}
		}
		result.Showdown[seat] = hand
	}
	result.Elapsed = time.Since(start)
	return result, nil
}
