package demo

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"cappedpoker/poker"
)

//go:embed index.html style.css
var assets embed.FS

var tablePage = template.Must(template.New("index.html").ParseFS(assets, "index.html"))

type App struct {
	mu      sync.Mutex
	table   *poker.Table
	mode    scenario
	version uint64
	history []string
}

func New() (*App, error) {
	app := &App{}
	if err := app.restart("cap"); err != nil {
		return nil, err
	}
	app.version = 1
	return app, nil
}

func (a *App) restart(id string) error {
	mode, table, err := start(id)
	if err != nil {
		return err
	}
	a.table, a.mode = table, mode
	a.history = []string{"Hand 1 dealt. Blinds posted."}
	return nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host != "localhost" && host != "127.0.0.1" {
		http.Error(w, "Open the demo on localhost.", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; form-action 'self'")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/style.css":
		css, err := assets.ReadFile("style.css")
		if err != nil {
			http.Error(w, "Styles unavailable.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(css)
	case r.Method == http.MethodGet && r.URL.Path == "/":
		a.render(w, http.StatusOK, "")
	case r.Method == http.MethodPost && r.URL.Path == "/table":
		a.post(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) post(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if (origin != "" && origin != "http://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Cross-site requests are blocked.", http.StatusForbidden)
		return
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Unsupported form encoding.", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form.", http.StatusBadRequest)
		return
	}
	version, err := strconv.ParseUint(r.PostForm.Get("version"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid table version.", http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	if version != a.version {
		a.mu.Unlock()
		a.render(w, http.StatusConflict, "The table has moved on. Use the current controls below.")
		return
	}
	err = a.apply(r)
	if err == nil {
		a.version++
	}
	a.mu.Unlock()
	if err != nil {
		a.render(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) apply(r *http.Request) error {
	view := a.table.View(poker.NoSeat)
	switch r.PostForm.Get("command") {
	case "restart":
		return a.restart(r.PostForm.Get("scenario"))
	case "next":
		if view.Street != poker.Settled {
			return poker.ErrHandActive
		}
		deck, err := poker.ShuffleDeck()
		if err != nil {
			return err
		}
		if err := a.table.Deal(deck); err != nil {
			return err
		}
		a.history = []string{fmt.Sprintf("Hand %d dealt. Blinds posted.", view.Hand+1)}
		return nil
	case "rebuy":
		seat, err := strconv.Atoi(r.PostForm.Get("seat"))
		if err != nil || seat < 1 || seat > len(view.Seats) {
			return poker.ErrSeat
		}
		if err := a.table.Rebuy(seat - 1); err != nil {
			return err
		}
		a.history = append(a.history, fmt.Sprintf("Seat %d rebuys for %d.", seat, view.Config.BuyIn))
		return nil
	case "act":
		target, err := strconv.ParseInt(r.PostForm.Get("target"), 10, 64)
		if err != nil {
			return poker.ErrIllegalMove
		}
		action := poker.Action{Kind: poker.ActionKind(r.PostForm.Get("kind")), Target: poker.Chips(target)}
		for _, option := range a.table.LegalActions(view.Actor) {
			if option.Action == action {
				if err := a.table.Act(view.Actor, action); err != nil {
					return err
				}
				a.history = append(a.history, fmt.Sprintf("Seat %d · %s", view.Actor+1, actionLabel(option)))
				street := a.table.View(poker.NoSeat).Street
				if street != view.Street {
					a.history = append(a.history, streetLabel(street))
				}
				return nil
			}
		}
		return poker.ErrIllegalMove
	default:
		return errors.New("unknown table command")
	}
}

func (a *App) render(w http.ResponseWriter, status int, notice string) {
	a.mu.Lock()
	view := a.table.View(poker.NoSeat)
	if view.Actor != poker.NoSeat {
		view = a.table.View(view.Actor)
	}
	data := buildPage(view, a.mode, a.version, slices.Clone(a.history), notice)
	a.mu.Unlock()

	var body bytes.Buffer
	if err := tablePage.Execute(&body, data); err != nil {
		http.Error(w, "Table unavailable.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}
