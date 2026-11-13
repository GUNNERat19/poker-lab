// Package privatedeal is a local research prototype for a verifiable Hold'em deal.
package privatedeal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/group/nist"
	"go.dedis.ch/kyber/v3/proof"
	"go.dedis.ch/kyber/v3/shuffle"

	"cappedpoker/poker"
)

const cards = 52
const maxShuffleProof = 64 << 10

var suite = nist.NewBlakeSHA256P256()
var keyProof = proof.Rep("K", "x", "G")
var shareProof = proof.And(keyProof, proof.Rep("D", "x", "X"))

type publicKey struct {
	point kyber.Point
	proof []byte
}

type player struct {
	key kyber.Scalar
}

type encryptedDeck struct {
	x, y []kyber.Point
}

type mix struct {
	deck  encryptedDeck
	proof []byte
}

type share struct {
	point kyber.Point
	proof []byte
}

func label(round string, operation string, seat, position int, points ...kyber.Point) string {
	hash := sha256.New()
	for _, p := range points {
		// Inputs are initialized P-256 points with a fixed-size encoding.
		p.MarshalTo(hash)
	}
	return fmt.Sprintf("poker-private-deal-v1/%s/%s/%d/%d/%s", round, operation, seat, position, hex.EncodeToString(hash.Sum(nil)))
}

func newPlayer(round string, seat int) (player, publicKey, error) {
	x := suite.Scalar().Pick(suite.RandomStream())
	for x.Equal(suite.Scalar().Zero()) {
		x.Pick(suite.RandomStream())
	}
	k := suite.Point().Mul(x, nil)
	points := map[string]kyber.Point{"G": suite.Point().Base(), "K": k}
	p, err := proof.HashProve(suite, label(round, "key", seat, -1, k),
		keyProof.Prover(suite, map[string]kyber.Scalar{"x": x}, points, nil))
	return player{x}, publicKey{k, p}, err
}

func cardPoint(i int) kyber.Point {
	return suite.Point().Mul(suite.Scalar().SetInt64(int64(i+1)), nil)
}

func canonicalDeck(key kyber.Point) encryptedDeck {
	deck := encryptedDeck{make([]kyber.Point, cards), make([]kyber.Point, cards)}
	for i := range cards {
		// The initial deck and its masks are public. Privacy starts after
		// at least one honest player privately rerandomizes and shuffles it.
		r := suite.Scalar().SetInt64(int64(i + 1))
		deck.x[i] = suite.Point().Mul(r, nil)
		deck.y[i] = suite.Point().Add(cardPoint(i), suite.Point().Mul(r, key))
	}
	return deck
}

func shuffleLabel(round string, seat int, key kyber.Point, before, after encryptedDeck) string {
	points := []kyber.Point{key}
	for _, list := range [][]kyber.Point{before.x, before.y, after.x, after.y} {
		points = append(points, list...)
	}
	return label(round, "shuffle", seat, -1, points...)
}

func makeMix(round string, seat int, key kyber.Point, before encryptedDeck) (mix, error) {
	x, y, prover := shuffle.Shuffle(suite, nil, key, before.x, before.y, suite.RandomStream())
	after := encryptedDeck{x, y}
	p, err := proof.HashProve(suite, shuffleLabel(round, seat, key, before, after), prover)
	return mix{after, p}, err
}

func makeShare(round string, seat, position int, p player, key kyber.Point, deck encryptedDeck) (share, error) {
	x := deck.x[position]
	d := suite.Point().Mul(p.key, x)
	points := map[string]kyber.Point{"G": suite.Point().Base(), "K": key, "X": x, "D": d}
	encoded, err := proof.HashProve(suite, label(round, "share", seat, position, key, x, d),
		shareProof.Prover(suite, map[string]kyber.Scalar{"x": p.key}, points, nil))
	return share{d, encoded}, err
}

func verifyShare(round string, seat, position int, key kyber.Point, deck encryptedDeck, s share) error {
	if s.point == nil || len(s.proof) == 0 || len(s.proof) > 512 {
		return fmt.Errorf("invalid share for seat %d", seat+1)
	}
	x := deck.x[position]
	points := map[string]kyber.Point{"G": suite.Point().Base(), "K": key, "X": x, "D": s.point}
	return proof.HashVerify(suite, label(round, "share", seat, position, key, x, s.point),
		shareProof.Verifier(suite, points), s.proof)
}

func decodeCard(deck encryptedDeck, position int, shares []kyber.Point) (poker.Card, error) {
	point := deck.y[position].Clone()
	for _, s := range shares {
		point.Sub(point, s)
	}
	for i := range cards {
		if point.Equal(cardPoint(i)) {
			return poker.Card(i), nil
		}
	}
	return 0, fmt.Errorf("decryption did not produce a card")
}
