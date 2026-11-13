package live

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"cappedpoker/poker"
)

func TestSidePotPracticeWalkthrough(t *testing.T) {
	server := New(context.Background(), time.Minute)
	t.Cleanup(server.Close)
	host, _ := enterRoom(t, server, "/live/rooms?scenario=side-pot", http.StatusCreated)
	join := "/live/rooms/" + host.Room + "/join"
	third, _ := enterRoom(t, server, join+"?seat=3", http.StatusOK)
	r := server.findRoom(host.Room)
	if third.Seat != 2 || readState(t, r, 0).CanDeal {
		t.Fatal("the practice room must honor the requested seat and wait for three players")
	}
	guest, _ := enterRoom(t, server, join+"?seat=2", http.StatusOK)
	if guest.Seat != 1 {
		t.Fatal("guest did not get the requested open seat")
	}
	play := func(seat int, kind string, action poker.ActionKind, target poker.Chips) {
		t.Helper()
		version := readState(t, r, seat).Version
		cmd := command{ID: strconv.FormatUint(version, 10), Version: version, Kind: kind, Action: action, Target: target}
		if result := sendCommand(t, r, seat, cmd); result.Error != "" {
			t.Fatalf("seat %d %s %s: %s", seat+1, kind, action, result.Error)
		}
	}
	play(0, "deal", "", 0)
	play(0, "act", poker.Call, 0)
	play(1, "act", poker.Call, 0)
	play(2, "act", poker.Check, 0)
	play(1, "act", poker.Check, 0)
	play(2, "act", poker.Bet, 5)
	play(0, "act", poker.Call, 0)
	play(1, "act", poker.Fold, 0)

	first := readState(t, r, 0)
	if first.Street != poker.Settled || first.Seats[0].Stack != 12 || first.Seats[1].Stack != 5 || first.Seats[2].Stack != 13 {
		t.Fatalf("unexpected opening stacks for hand two: %+v", first.Seats)
	}
	play(0, "deal", "", 0)
	if got := readState(t, r, 1).BestHand; got != "Pocket Aces" {
		t.Fatalf("seat 2 starts with %q", got)
	}
	play(1, "act", poker.Call, 0)
	play(2, "act", poker.Raise, 10)
	play(0, "act", poker.Call, 0)
	play(2, "act", poker.AllIn, 0)
	play(0, "act", poker.Call, 0)

	second := readState(t, r, 0)
	pots := []pot{{Amount: 15, Winners: []int{1}}, {Amount: 14, Winners: []int{2}}}
	if !reflect.DeepEqual(second.Pots, pots) || !reflect.DeepEqual(second.Refunds, []poker.Chips{0, 0, 1}) {
		t.Fatalf("unexpected settlement: pots %+v, refunds %v", second.Pots, second.Refunds)
	}
	if !second.CanRebuy || second.Seats[0].Stack != 0 {
		t.Fatal("the busted seat should be able to rebuy")
	}
	play(0, "rebuy", "", 0)
	if got := readState(t, r, 0).Seats[0].Stack; got != 10 {
		t.Fatalf("stack after rebuy: %d", got)
	}
	play(0, "deal", "", 0)
	if got := readState(t, r, 0).Hand; got != 3 {
		t.Fatalf("hand after rebuy: %d", got)
	}
}
