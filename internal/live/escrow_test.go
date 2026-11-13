package live

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"cappedpoker/poker"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

type rpcTransport func(*http.Request) (*http.Response, error)

func (f rpcTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEscrowCheckpointGate(t *testing.T) {
	keys := []string{strings.Repeat("0", 63) + "1", strings.Repeat("0", 63) + "2"}
	roster := make([]common.Address, len(keys))
	for i, key := range keys {
		private, err := crypto.HexToECDSA(key)
		if err != nil {
			t.Fatal(err)
		}
		roster[i] = crypto.PubkeyToAddress(private.PublicKey)
	}
	sign := func(seat int, digest common.Hash) string {
		t.Helper()
		private, err := crypto.HexToECDSA(keys[seat])
		if err != nil {
			t.Fatal(err)
		}
		signature, err := crypto.Sign(digest[:], private)
		if err != nil {
			t.Fatal(err)
		}
		signature[64] += 27
		return hexutil.Encode(signature)
	}
	source := &escrowSource{
		escrowSession: escrowSession{ChainID: localAnvil, Contract: common.HexToAddress("0x1234"),
			TableID: hashText("table"), SessionID: hashText("session"), RulesHash: hashText(poker.RulesVersion),
			Roster: roster, BuyInWei: "5000"},
		endpoint: "http://127.0.0.1",
		client: &http.Client{Transport: rpcTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{"result":"0x` + hex.EncodeToString(uintWord(1)) + `"}`))}, nil
		})},
	}
	ctx, cancel := context.WithCancel(context.Background())
	var store *postgresStore
	databaseURL := os.Getenv("POKER_TEST_DATABASE_URL")
	if databaseURL != "" {
		var err error
		store, err = openStore(ctx, databaseURL, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
	}
	id, err := secret(9)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	r, err := newRoom(ctx, id, time.Minute, "host-token", store, escrowMode, source)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		<-r.done
		if store != nil {
			store.pool.Close()
		}
	})
	roomID := hashText(id)
	join := func(wallet int) entry {
		return inRoom(t, r, func(s *roomState) entry {
			return s.join("", 1, sign(wallet, source.bindingDigest(roomID, 1)))
		})
	}
	if wrong := join(0); wrong.err == nil {
		t.Fatal("wrong wallet reserved an escrow seat")
	}
	if guest := join(1); guest.err != nil || guest.seat != 1 {
		t.Fatalf("guest join: %+v", guest)
	}
	play := func(seat int, cmd command) ack {
		t.Helper()
		cmd.Version = readState(t, r, seat).Version
		cmd.ID = hex.EncodeToString(uintWord(cmd.Version))
		return sendCommand(t, r, seat, cmd)
	}
	accepted := func(seat int, cmd command) {
		t.Helper()
		if got := play(seat, cmd); got.Error != "" {
			t.Fatalf("seat %d %s: %s", seat, cmd.Kind, got.Error)
		}
	}
	accepted(0, command{Kind: "bind-wallet", Signature: sign(0, source.bindingDigest(roomID, 0))})
	accepted(0, command{Kind: "deal"})
	accepted(0, command{Kind: "act", Action: poker.Fold})
	pending := readState(t, r, 0).Escrow.Pending
	if pending == nil || !slices.Equal(pending.Balances, []string{"4980", "5020"}) {
		t.Fatalf("settled hand did not propose matching balances: %+v", pending)
	}
	if readState(t, r, poker.NoSeat).Escrow.Pending != nil {
		t.Fatal("observer received a private checkpoint")
	}
	digest := source.checkpointDigest(pending)
	for seat := range roster {
		accepted(seat, command{Kind: "sign-checkpoint", Sequence: pending.Sequence, Signature: sign(seat, digest)})
		if got := play(0, command{Kind: "deal"}); got.Error == "" {
			t.Fatal("dealt before all players saved the signed checkpoint")
		}
	}
	for seat := range roster {
		accepted(seat, command{Kind: "save-checkpoint", Sequence: pending.Sequence})
	}
	state := readState(t, r, 0)
	if !state.CanDeal || state.Escrow.Latest == nil || slices.Contains(state.Escrow.Latest.Signatures, "") {
		t.Fatal("fully saved checkpoint did not unlock the next deal")
	}
	if store != nil {
		replayed, err := store.load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := replayed[id]; !allSeats(got.saved) || got.latest == nil ||
			!slices.Equal(got.latest.Signatures, state.Escrow.Latest.Signatures) {
			t.Fatal("signed checkpoint and save receipts were not replayed")
		}
	}
}
