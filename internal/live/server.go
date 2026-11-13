package live

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"cappedpoker/poker"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const roomLimit = 64
const seatCookie = "poker_seat"

//go:embed index.html live.js
var assets embed.FS

type Server struct {
	ctx          context.Context
	cancel       context.CancelFunc
	store        *postgresStore
	escrowSource *escrowSource
	escrowUsed   bool
	turnTime     time.Duration
	mux          *http.ServeMux
	mu           sync.RWMutex
	rooms        map[string]*room
}

func New(ctx context.Context, turnTime time.Duration) *Server {
	ctx, cancel := context.WithCancel(ctx)
	s := &Server{ctx: ctx, cancel: cancel, turnTime: turnTime, mux: http.NewServeMux(), rooms: make(map[string]*room)}
	s.mux.HandleFunc("GET /live/{$}", serveAsset("index.html", "text/html; charset=utf-8"))
	s.mux.HandleFunc("GET /live/live.js", serveAsset("live.js", "text/javascript; charset=utf-8"))
	s.mux.HandleFunc("POST /live/rooms", s.create)
	s.mux.HandleFunc("GET /live/rooms/escrow", s.escrowAvailable)
	s.mux.HandleFunc("POST /live/rooms/{room}/join", s.join)
	s.mux.HandleFunc("GET /live/rooms/{room}/socket", s.socket)
	return s
}

// Close stops room timers and releases the database pool.
func (s *Server) Close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, room := range s.rooms {
		<-room.done
	}
	if s.store != nil {
		s.store.pool.Close()
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host != "localhost" && host != "127.0.0.1" {
		http.Error(w, "Open the demo on localhost.", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		http.Error(w, "Cross-origin requests are blocked.", http.StatusForbidden)
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" && (r.Method == http.MethodPost || r.Header.Get("Upgrade") != "") {
		http.Error(w, "Cross-site requests are blocked.", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws://"+r.Host+"; frame-ancestors 'none'; form-action 'self'")
	s.mux.ServeHTTP(w, r)
}

func serveAsset(name, contentType string) http.HandlerFunc {
	body, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}
}

type roomResponse struct {
	Room string `json:"room"`
	Seat int    `json:"seat"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	scenario := r.URL.Query().Get("scenario")
	if scenario != "" && scenario != sidePotPractice && scenario != escrowMode {
		http.Error(w, "Unknown practice scenario.", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	var source *escrowSource
	if scenario == escrowMode {
		if s.escrowSource == nil || s.escrowUsed {
			s.mu.Unlock()
			http.Error(w, "The configured escrow is unavailable or already has a room.", http.StatusConflict)
			return
		}
		source = s.escrowSource
		if err := source.requireActive(r.Context()); err != nil {
			s.mu.Unlock()
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		http.Error(w, "Server is shutting down.", http.StatusServiceUnavailable)
		return
	}
	if len(s.rooms) >= roomLimit {
		s.mu.Unlock()
		http.Error(w, "This demo server has reached its room limit.", http.StatusServiceUnavailable)
		return
	}
	id, err := secret(9)
	if err != nil || s.rooms[id] != nil {
		s.mu.Unlock()
		http.Error(w, "Could not create a room. Try again.", http.StatusInternalServerError)
		return
	}
	token, err := secret(32)
	if err != nil {
		s.mu.Unlock()
		http.Error(w, "Could not create a seat.", http.StatusInternalServerError)
		return
	}
	room, err := newRoom(s.ctx, id, s.turnTime, token, s.store, scenario, source)
	if err != nil {
		s.mu.Unlock()
		http.Error(w, "Could not save the new table.", http.StatusServiceUnavailable)
		return
	}
	s.rooms[id] = room
	s.escrowUsed = s.escrowUsed || source != nil
	s.mu.Unlock()
	writeEntry(w, id, token, 0, http.StatusCreated)
}

func (s *Server) escrowAvailable(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	available := s.escrowSource != nil && !s.escrowUsed
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"available": available})
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("room")
	room := s.findRoom(id)
	if room == nil {
		http.Error(w, "Room not found.", http.StatusNotFound)
		return
	}
	preferred := poker.NoSeat
	if requested := r.URL.Query().Get("seat"); requested != "" {
		seat, err := strconv.Atoi(requested)
		if err != nil || seat < 1 || seat > 6 {
			http.Error(w, "Choose an open seat.", http.StatusBadRequest)
			return
		}
		preferred = seat - 1
	}
	var proof struct {
		Signature string `json:"signature"`
	}
	if r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 512)
		if err := json.NewDecoder(r.Body).Decode(&proof); err != nil {
			http.Error(w, "Invalid wallet proof.", http.StatusBadRequest)
			return
		}
	}
	result, err := ask(r.Context(), room, func(state *roomState) entry {
		return state.join(cookieToken(r), preferred, proof.Signature)
	})
	if err != nil {
		http.Error(w, "Room unavailable.", http.StatusServiceUnavailable)
		return
	}
	if result.err != nil {
		status := http.StatusConflict
		if errors.Is(result.err, errStoragePaused) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, result.err.Error(), status)
		return
	}
	writeEntry(w, id, result.token, result.seat, http.StatusOK)
}

func writeEntry(w http.ResponseWriter, id, token string, seat, status int) {
	http.SetCookie(w, &http.Cookie{
		Name: seatCookie, Value: token, Path: "/live/rooms/" + id + "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(roomResponse{Room: id, Seat: seat})
}

func cookieToken(r *http.Request) string {
	cookie, err := r.Cookie(seatCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) findRoom(id string) *room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rooms[id]
}

func (s *Server) socket(w http.ResponseWriter, r *http.Request) {
	room := s.findRoom(r.PathValue("room"))
	if room == nil {
		http.Error(w, "Room not found.", http.StatusNotFound)
		return
	}
	token := cookieToken(r)
	sub, err := ask(r.Context(), room, func(state *roomState) subscription {
		return state.subscribe(token)
	})
	if err != nil || sub.states == nil {
		http.Error(w, "Room connections unavailable.", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		_, _ = ask(s.ctx, room, func(state *roomState) bool {
			delete(state.subscribers, sub.states)
			return true
		})
	}()

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(2048)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case state := <-sub.states:
				if err := writeSocket(ctx, conn, state); err != nil {
					return
				}
			}
		}
	}()
	for {
		var cmd command
		if err := wsjson.Read(ctx, conn, &cmd); err != nil {
			return
		}
		response, err := ask(ctx, room, func(state *roomState) ack {
			return state.apply(sub.seat, cmd)
		})
		if err != nil || writeSocket(ctx, conn, response) != nil {
			return
		}
	}
}

func writeSocket(ctx context.Context, conn *websocket.Conn, value any) error {
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(deadline, conn, value)
}
