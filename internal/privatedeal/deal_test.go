package privatedeal

import (
	"errors"
	"testing"

	"cappedpoker/poker"
)

func TestPrivateDeal(t *testing.T) {
	for _, seats := range []int{2, 6} {
		got, err := Simulate(seats, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.ShuffleCount != seats || len(got.HoleCards) != seats || len(got.Board) != 5 || len(got.Showdown) != 2 {
			t.Fatalf("wrong deal shape: %+v", got)
		}
		seen := make(map[poker.Card]bool)
		add := func(card poker.Card) {
			t.Helper()
			if card >= 52 || seen[card] {
				t.Fatalf("invalid or repeated card: %d", card)
			}
			seen[card] = true
		}
		for seat, hand := range got.HoleCards {
			add(hand[0])
			add(hand[1])
			revealed, ok := got.Showdown[seat]
			if ok != (seat < 2) || (ok && revealed != hand) {
				t.Fatalf("wrong showdown reveal for seat %d", seat)
			}
		}
		for _, card := range got.Board {
			add(card)
		}
	}
}

func TestWithheldShuffleOrShareAborts(t *testing.T) {
	for _, stage := range []string{"shuffle", "hole share", "board share", "showdown share"} {
		t.Run(stage, func(t *testing.T) {
			got, err := Simulate(3, func(step Step) bool { return step.Stage == stage && step.Seat == 2 })
			if !errors.Is(err, ErrAborted) || got.HoleCards != nil || got.Board != nil || got.Showdown != nil {
				t.Fatalf("missing participant should abort: %+v, %v", got, err)
			}
		})
	}
}

func TestProofsRejectTamperingAndReplay(t *testing.T) {
	const round = "test-round"
	first, k1, err := newPlayer(round, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, k2, err := newPlayer(round, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newDealer("another-round", []publicKey{k1, k2}); err == nil {
		t.Fatal("accepted replayed key proof")
	}
	d, err := newDealer(round, []publicKey{k1, k2})
	if err != nil {
		t.Fatal(err)
	}
	m, err := makeMix(round, 0, d.key, d.deck)
	if err != nil {
		t.Fatal(err)
	}
	m.deck.y[0], m.deck.y[1] = m.deck.y[1], m.deck.y[0]
	if err := d.acceptMix(0, m); err == nil || d.mixed != 0 {
		t.Fatal("accepted tampered deck")
	}
	m.deck.y[0], m.deck.y[1] = m.deck.y[1], m.deck.y[0]
	if err := d.acceptMix(0, m); err != nil {
		t.Fatal(err)
	}
	if err := d.acceptMix(0, m); err == nil {
		t.Fatal("accepted replayed shuffle")
	}

	s, err := makeShare(round, 0, 0, first, d.keys[0], d.deck)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyShare(round, 0, 0, d.keys[0], d.deck, s); err != nil {
		t.Fatal(err)
	}
	if err := verifyShare(round, 0, 1, d.keys[0], d.deck, s); err == nil {
		t.Fatal("accepted share for the wrong position")
	}
	s.point = suite.Point().Add(s.point, suite.Point().Base())
	if err := verifyShare(round, 0, 0, d.keys[0], d.deck, s); err == nil {
		t.Fatal("accepted tampered share")
	}
}
