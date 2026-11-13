package live

import (
	"context"
	"strings"
	"testing"
	"time"

	"cappedpoker/poker"
)

func tableRoom(t *testing.T) *room {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := newRoom(ctx, "test-room", time.Minute, "host-token", nil, "", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		<-r.done
	})
	guest := inRoom(t, r, func(state *roomState) entry { return state.join("", poker.NoSeat, "") })
	if guest.err != nil || guest.seat != 1 {
		t.Fatalf("could not seat guest: %+v", guest)
	}
	return r
}

func inRoom[T any](t *testing.T, r *room, fn func(*roomState) T) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, err := ask(ctx, r, fn)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func sendCommand(t *testing.T, r *room, seat int, cmd command) ack {
	t.Helper()
	return inRoom(t, r, func(state *roomState) ack { return state.apply(seat, cmd) })
}

func readState(t *testing.T, r *room, seat int) snapshot {
	t.Helper()
	return inRoom(t, r, func(state *roomState) snapshot { return state.snapshot(seat) })
}

func TestRoomRejectsUnauthorizedAndRepeatedActions(t *testing.T) {
	r := tableRoom(t)
	initial := readState(t, r, 0)
	deal := command{ID: "deal-1", Version: initial.Version, Kind: "deal"}
	if got := sendCommand(t, r, 1, deal); got.Error == "" {
		t.Fatal("guest dealt a hand")
	}
	if got := sendCommand(t, r, 0, deal); got.Error != "" {
		t.Fatal(got.Error)
	}
	host, guest, observer := readState(t, r, 0), readState(t, r, 1), readState(t, r, poker.NoSeat)
	if len(host.Seats[0].Cards) != 2 || len(host.Seats[1].Cards) != 0 ||
		len(guest.Seats[1].Cards) != 2 || len(guest.Seats[0].Cards) != 0 ||
		len(observer.Seats[0].Cards) != 0 || len(observer.Seats[1].Cards) != 0 {
		t.Fatal("incorrect card visibility")
	}
	if host.BestHand == "" || guest.BestHand == "" || observer.BestHand != "" {
		t.Fatal("each player should have a private hand description even when waiting for their turn")
	}
	call := command{ID: "call-1", Version: host.Version, Kind: "act", Action: poker.Call}
	if got := sendCommand(t, r, poker.NoSeat, call); got.Error == "" {
		t.Fatal("observer submitted an action")
	}
	if got := sendCommand(t, r, 1, call); got.Error == "" {
		t.Fatal("guest acted out of turn")
	}
	accepted := sendCommand(t, r, 0, call)
	if accepted.Error != "" || accepted.Version != call.Version+1 {
		t.Fatalf("call receipt: %+v", accepted)
	}
	if duplicate := sendCommand(t, r, 0, call); duplicate != accepted {
		t.Fatalf("duplicate did not return original receipt: %+v", duplicate)
	}
	if state := readState(t, r, 0); state.Pot != 10 || state.Version != accepted.Version || state.Actor != 1 {
		t.Fatalf("replay changed table: %+v", state)
	}
	call.Action = poker.Fold
	if got := sendCommand(t, r, 0, call); !strings.Contains(got.Error, "already used") {
		t.Fatalf("ID reused with different action: %+v", got)
	}
	call.ID = "new-id"
	if got := sendCommand(t, r, 0, call); !strings.Contains(got.Error, "moved on") {
		t.Fatalf("stale state accepted: %+v", got)
	}
}

func TestTurnExpiresWhileNobodyIsConnected(t *testing.T) {
	r := tableRoom(t)
	lobby := readState(t, r, 0)
	if got := sendCommand(t, r, 0, command{ID: "deal-1", Version: lobby.Version, Kind: "deal"}); got.Error != "" {
		t.Fatal(got.Error)
	}
	before := readState(t, r, 0)
	if before.Actor != 0 || before.Deadline == nil {
		t.Fatalf("first turn: %+v", before)
	}
	inRoom(t, r, func(state *roomState) bool {
		state.deadline = time.Now().Add(-time.Millisecond)
		return true
	})
	late := command{ID: "late-call", Version: before.Version, Kind: "act", Action: poker.Call}
	if got := sendCommand(t, r, 0, late); !strings.Contains(got.Error, "moved on") {
		t.Fatalf("late call was not rejected: %+v", got)
	}
	after := readState(t, r, poker.NoSeat)
	if after.Street != poker.Settled || after.Deadline != nil || !after.Seats[0].Folded {
		t.Fatalf("expired heads-up small blind should fold: %+v", after)
	}
	if got := readState(t, r, 0).BestHand; got != "" {
		t.Fatalf("folded player still has a hand description: %q", got)
	}
}
