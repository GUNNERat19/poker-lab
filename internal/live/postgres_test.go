package live

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"cappedpoker/poker"
)

func TestPostgresRoomRecovery(t *testing.T) {
	databaseURL := os.Getenv("POKER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set POKER_TEST_DATABASE_URL to a dedicated PostgreSQL test database")
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	ctx := context.Background()
	first, err := OpenPersistent(ctx, time.Minute, databaseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	host, hostCookie := enterRoom(t, first, "/live/rooms", http.StatusCreated)
	path := "/live/rooms/" + host.Room + "/join"
	guest, guestCookie := enterRoom(t, first, path, http.StatusOK)
	if host.Seat != 0 || guest.Seat != 1 {
		t.Fatalf("host and guest seats: %d, %d", host.Seat, guest.Seat)
	}

	r := first.findRoom(host.Room)
	deal := command{ID: "deal", Version: readState(t, r, 0).Version, Kind: "deal"}
	if got := sendCommand(t, r, 0, deal); got.Error != "" {
		t.Fatal(got.Error)
	}
	call := command{ID: "call", Version: readState(t, r, 0).Version, Kind: "act", Action: poker.Call}
	callReceipt := sendCommand(t, r, 0, call)
	if callReceipt.Error != "" {
		t.Fatal(callReceipt.Error)
	}
	before := [2]snapshot{readState(t, r, 0), readState(t, r, 1)}
	first.Close()

	wrongKey := make([]byte, 32)
	wrongKey[0] = 1
	if bad, err := OpenPersistent(ctx, time.Minute, databaseURL, base64.StdEncoding.EncodeToString(wrongKey)); err == nil {
		bad.Close()
		t.Fatal("room loaded with the wrong storage key")
	}
	second, err := OpenPersistent(ctx, 2*time.Minute, databaseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	r = second.findRoom(host.Room)
	if r == nil {
		t.Fatal("room missing after restart")
	}
	for seat, cookie := range []*http.Cookie{hostCookie, guestCookie} {
		identity := inRoom(t, r, func(state *roomState) int { return state.seatFor(cookie.Value) })
		if identity != seat {
			t.Fatalf("seat cookie restored as seat %d, expected %d", identity, seat)
		}
		if got := readState(t, r, seat); !reflect.DeepEqual(got, before[seat]) {
			t.Fatalf("seat %d did not resume with its original snapshot: got %+v, want %+v", seat, got, before[seat])
		}
	}
	if got := sendCommand(t, r, 0, call); got != callReceipt {
		t.Fatalf("replay did not return the original receipt: %+v", got)
	}
	if observer := readState(t, r, poker.NoSeat); observer.BestHand != "" || len(observer.Seats[0].Cards) != 0 {
		t.Fatal("observer saw private cards after restart")
	}

	for i := range 7 {
		state := readState(t, r, poker.NoSeat)
		check := command{ID: "check-" + string(rune('0'+i)), Version: state.Version, Kind: "act", Action: poker.Check}
		if got := sendCommand(t, r, state.Actor, check); got.Error != "" {
			t.Fatal(got.Error)
		}
	}
	after := readState(t, r, poker.NoSeat)
	if after.Street != poker.Settled || after.Pot != 10 || len(after.Board) != 5 {
		t.Fatalf("recovered hand did not settle: %+v", after)
	}
	var issued, wagered, paid int64
	err = second.store.pool.QueryRow(ctx, `SELECT
		coalesce(sum(amount) FILTER (WHERE kind = 'buy_in'), 0)::bigint,
		coalesce(sum(amount) FILTER (WHERE kind = 'wager'), 0)::bigint,
		coalesce(sum(amount) FILTER (WHERE kind = 'award'), 0)::bigint
		FROM poker_lab.chip_journal WHERE room_id = $1`, host.Room).Scan(&issued, &wagered, &paid)
	if err != nil || issued != 1000 || wagered != 10 || paid != 10 {
		t.Fatalf("chip journal: issued %d, wagered %d, paid %d: %v", issued, wagered, paid, err)
	}

	// A rejected journal row must roll back its event as well.
	version := after.Version + 1
	if err := second.store.append(ctx, host.Room, version, roomEvent{Kind: "test"}, []chipMove{{"invalid", 0, 1}}); err == nil {
		t.Fatal("expected the invalid chip movement to fail")
	}
	var exists bool
	err = second.store.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM poker_lab.room_events WHERE room_id = $1 AND version = $2
	)`, host.Room, version).Scan(&exists)
	if err != nil || exists {
		t.Fatalf("event survived journal rollback: exists %v, error %v", exists, err)
	}
}

func enterRoom(t *testing.T, server *Server, path string, status int) (roomResponse, *http.Cookie) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080"+path, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
	}
	var entry roomResponse
	if err := json.Unmarshal(response.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatalf("seat cookie: %v", cookies)
	}
	return entry, cookies[0]
}
