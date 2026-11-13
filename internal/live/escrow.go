package live

import (
	"encoding/json"
	"errors"
	"math/big"
	"slices"

	"cappedpoker/poker"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

var domainType = hashText("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
var checkpointType = hashText("Checkpoint(bytes32 tableId,bytes32 sessionId,bytes32 rulesHash,uint256 sequence,uint256[] balances,bytes32 transcriptHash)")
var bindingType = hashText("SeatBinding(bytes32 tableId,bytes32 sessionId,bytes32 roomId,uint256 seat)")

type checkpoint struct {
	Sequence       uint64      `json:"sequence"`
	Balances       []string    `json:"balances"`
	TranscriptHash common.Hash `json:"transcriptHash"`
	Signatures     []string    `json:"signatures"`
}

type escrowView struct {
	escrowSession
	RoomID    common.Hash `json:"roomId"`
	Bound     []bool      `json:"bound"`
	Saved     []bool      `json:"saved"`
	Pending   *checkpoint `json:"pending,omitempty"`
	Latest    *checkpoint `json:"latest,omitempty"`
	Connected bool        `json:"connected"`
}

func hashText(s string) common.Hash { return crypto.Keccak256Hash([]byte(s)) }

func uintWord(n uint64) []byte { return new(big.Int).SetUint64(n).FillBytes(make([]byte, 32)) }

func (s escrowSession) typedDigest(structHash common.Hash) common.Hash {
	name, version := hashText("Poker Lab Escrow"), hashText("1")
	domain := crypto.Keccak256Hash(domainType[:], name[:], version[:], uintWord(s.ChainID), common.LeftPadBytes(s.Contract[:], 32))
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, domain[:], structHash[:])
}

func (s escrowSession) bindingDigest(roomID common.Hash, seat int) common.Hash {
	return s.typedDigest(crypto.Keccak256Hash(bindingType[:], s.TableID[:], s.SessionID[:], roomID[:], uintWord(uint64(seat))))
}

func (s escrowSession) checkpointDigest(c *checkpoint) common.Hash {
	packed := make([]byte, 32*len(c.Balances))
	for i, balance := range c.Balances {
		n, _ := new(big.Int).SetString(balance, 10)
		n.FillBytes(packed[32*i : 32*(i+1)])
	}
	balancesHash := crypto.Keccak256Hash(packed)
	return s.typedDigest(crypto.Keccak256Hash(checkpointType[:], s.TableID[:], s.SessionID[:], s.RulesHash[:],
		uintWord(c.Sequence), balancesHash[:], c.TranscriptHash[:]))
}

func matchesWallet(wallet common.Address, digest common.Hash, signature string) bool {
	data, err := hexutil.Decode(signature)
	if err != nil || len(data) != 65 || (data[64] != 27 && data[64] != 28) {
		return false
	}
	data[64] -= 27
	r, s := new(big.Int).SetBytes(data[:32]), new(big.Int).SetBytes(data[32:64])
	if !crypto.ValidateSignatureValues(data[64], r, s, true) {
		return false
	}
	pub, err := crypto.SigToPub(digest[:], data)
	return err == nil && crypto.PubkeyToAddress(*pub) == wallet
}

func (s *roomState) escrowCommand(seat int, cmd command) error {
	if s.escrow == nil {
		return errors.New("this room does not use escrow")
	}
	switch cmd.Kind {
	case "bind-wallet":
		if s.bound[seat] || cmd.Sequence != 0 || !matchesWallet(s.escrow.Roster[seat], s.escrow.bindingDigest(hashText(s.id), seat), cmd.Signature) {
			return errors.New("sign with the wallet assigned to this seat")
		}
	case "sign-checkpoint":
		if !s.bound[seat] || s.pending == nil || cmd.Sequence != s.pending.Sequence || s.pending.Signatures[seat] != "" ||
			!matchesWallet(s.escrow.Roster[seat], s.escrow.checkpointDigest(s.pending), cmd.Signature) {
			return errors.New("signature does not match this seat and pending checkpoint")
		}
	case "save-checkpoint":
		if !s.bound[seat] || s.latest == nil || cmd.Sequence != s.latest.Sequence || cmd.Signature != "" || s.saved[seat] {
			return errors.New("checkpoint is unavailable or already saved")
		}
	}
	return nil
}

func allSeats(flags []bool) bool {
	for _, flag := range flags {
		if !flag {
			return false
		}
	}
	return true
}

func (s *roomState) escrowReady() bool {
	return s.escrow == nil || (s.escrowSource != nil && allSeats(s.bound) && s.pending == nil &&
		(s.latest == nil || allSeats(s.saved)))
}

func (s *roomState) acceptEscrow(event roomEvent) {
	switch event.Kind {
	case "create":
		if event.Escrow != nil {
			s.escrow = event.Escrow
			s.bound = make([]bool, len(s.escrow.Roster))
			s.saved = make([]bool, len(s.escrow.Roster))
		}
	case "bind-wallet":
		s.bound[event.Seat] = true
	case "join":
		if s.escrow != nil && event.Binding != "" {
			s.bound[event.Seat] = true
		}
	case "sign-checkpoint":
		s.pending.Signatures[event.Seat] = event.Command.Signature
		if !slices.Contains(s.pending.Signatures, "") {
			s.latest, s.pending = s.pending, nil
			clear(s.saved)
		}
	case "save-checkpoint":
		s.saved[event.Seat] = true
	case "deal", "act", "timeout":
		if s.escrow != nil {
			s.recordPublicAction(event)
		}
	}
}

func (s *roomState) recordPublicAction(event roomEvent) {
	if event.Kind == "deal" {
		s.transcript = common.Hash{}
	}
	view := s.table.View(poker.NoSeat)
	action := struct {
		Hand   uint64
		Kind   string
		Seat   int
		Action poker.ActionKind
		Target poker.Chips
		Board  []string
		Result *poker.Result
	}{Hand: view.Hand, Kind: event.Kind, Seat: event.Seat, Board: cardCodes(view.Board), Result: view.Result}
	if event.Command != nil {
		action.Action, action.Target = event.Command.Action, event.Command.Target
	}
	encoded, _ := json.Marshal(action) // All fields have fixed JSON encodings.
	s.transcript = crypto.Keccak256Hash(s.transcript[:], encoded)
	if view.Street != poker.Settled {
		return
	}
	buyIn, _ := new(big.Int).SetString(s.escrow.BuyInWei, 10)
	perChip := buyIn.Div(buyIn, big.NewInt(int64(view.Config.BuyIn)))
	balances := make([]string, len(view.Seats))
	for i, seat := range view.Seats {
		balances[i] = new(big.Int).Mul(perChip, big.NewInt(int64(seat.Stack))).String()
	}
	s.pending = &checkpoint{Sequence: view.Hand, Balances: balances, TranscriptHash: s.transcript, Signatures: make([]string, len(balances))}
}

func copyCheckpoint(c *checkpoint) *checkpoint {
	if c == nil {
		return nil
	}
	copy := *c
	copy.Balances, copy.Signatures = slices.Clone(c.Balances), slices.Clone(c.Signatures)
	return &copy
}
