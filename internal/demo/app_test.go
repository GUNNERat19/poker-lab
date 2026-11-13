package demo

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

type browser struct {
	t    *testing.T
	app  *App
	body string
}

var versionInput = regexp.MustCompile(`name="version" value="([0-9]+)"`)

func openTable(t *testing.T) *browser {
	t.Helper()
	app, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, app: app}
	b.reload()
	return b
}

func (b *browser) reload() {
	b.t.Helper()
	response := httptest.NewRecorder()
	b.app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil))
	if response.Code != http.StatusOK {
		b.t.Fatalf("GET /: status %d: %s", response.Code, response.Body)
	}
	b.body = response.Body.String()
}

func (b *browser) version() string {
	b.t.Helper()
	match := versionInput.FindStringSubmatch(b.body)
	if len(match) != 2 {
		b.t.Fatal("page is missing a table version")
	}
	return match[1]
}

func (b *browser) submit(values url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/table", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://localhost:8080")
	response := httptest.NewRecorder()
	b.app.ServeHTTP(response, request)
	return response
}

func (b *browser) click(values url.Values) {
	b.t.Helper()
	values.Set("version", b.version())
	if response := b.submit(values); response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		b.t.Fatalf("POST %v: status %d: %s", values, response.Code, response.Body)
	}
	b.reload()
}

func (b *browser) action(kind, target string) {
	b.t.Helper()
	b.click(url.Values{"command": {"act"}, "kind": {kind}, "target": {target}})
}

func (b *browser) see(text string) {
	b.t.Helper()
	if !strings.Contains(b.body, text) {
		b.t.Errorf("page does not contain %q", text)
	}
}

func TestRaiseCapWalkthrough(t *testing.T) {
	b := openTable(t)
	b.see(`id="action-title">Seat 4</h2>`)
	b.see(`aria-label="A of clubs"`)
	if strings.Contains(b.body, `aria-label="K of clubs"`) {
		t.Fatal("the page contains a non-acting player's private card")
	}
	stale := b.version()
	for range 3 {
		b.action("call", "0")
	}
	repeated := b.submit(url.Values{"version": {stale}, "command": {"act"}, "kind": {"call"}, "target": {"0"}})
	if repeated.Code != http.StatusConflict {
		t.Fatalf("repeated request should be stale, got %d", repeated.Code)
	}
	b.action("check", "0")
	b.see("<h2>Flop</h2>")
	b.action("bet", "20")
	b.see("Raise to 40")
	b.see("Raise to 80")
	b.see("Raise to 160")
	b.action("raise", "80")
	b.action("raise", "160")
	b.see("Street cap <strong>160")
	if strings.Contains(b.body, `class="action raise"`) {
		t.Fatal("a raise is available at the ceiling")
	}
	illegal := b.submit(url.Values{"version": {b.version()}, "command": {"act"}, "kind": {"raise"}, "target": {"320"}})
	if illegal.Code != http.StatusBadRequest {
		t.Fatalf("raise above the ceiling should be rejected, got %d", illegal.Code)
	}
	b.action("fold", "0")
	b.see("Call 140")
	b.action("call", "0")
	b.see("Call 80")
	b.action("call", "0")
	for range 6 {
		b.action("check", "0")
	}
	b.see("Main pot · 500 chips → Seat 4")
}

func TestRebuyWalkthrough(t *testing.T) {
	b := openTable(t)
	b.click(url.Values{"command": {"restart"}, "scenario": {"rebuy"}})
	b.see("Call 3")
	b.action("call", "0")
	b.see("Main pot · 10 chips → Seat 2")
	b.see("Rebuy Seat 1 · 5 chips")
	b.click(url.Values{"command": {"rebuy"}, "seat": {"1"}})
	b.see("Seat 1 rebuys for 5.")
	b.see("Deal next hand")
	b.click(url.Values{"command": {"next"}})
	b.see("Hand 2 dealt. Blinds posted.")
	b.see(`id="action-title">Seat 2</h2>`)
}
