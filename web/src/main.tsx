import { lazy, StrictMode, Suspense, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { roomPattern, roomRequest, useRoom, type Action, type Seat, type Street, type TableState } from "./room";
import "./style.css";

const EscrowPanel = lazy(() => import("./escrow").then(({ EscrowPanel }) => ({ default: EscrowPanel })));
const OfflineCheckpoint = lazy(() => import("./escrow").then(({ OfflineCheckpoint }) => ({ default: OfflineCheckpoint })));

const streets: Record<Street, string> = {
  waiting: "Waiting for players",
  preflop: "Preflop",
  flop: "Flop",
  turn: "Turn",
  river: "River",
  settled: "Hand complete",
};

function App() {
  const room = new URLSearchParams(location.search).get("room");
  return (
    <>
      <header className="topbar">
        <div className="wrap masthead">
          <a className="brand" href="/"><span aria-hidden="true">♠</span> Poker Lab</a>
          <span className="muted">Texas Hold’em · Play chips</span>
        </div>
      </header>
      <main className="wrap">
        {room && roomPattern.test(room) ? <Room room={room} /> : <Lobby invalidInvite={Boolean(room)} />}
      </main>
    </>
  );
}

function Lobby({ invalidInvite }: { invalidInvite: boolean }) {
  const [scenario, setScenario] = useState("");
  const [error, setError] = useState(invalidInvite ? "This invite link is invalid." : "");
  const [working, setWorking] = useState(false);
  const [escrowAvailable, setEscrowAvailable] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    fetch("/live/rooms/escrow", { signal: controller.signal })
      .then((response) => response.ok ? response.json() : null)
      .then((result) => setEscrowAvailable(result?.available === true))
      .catch(() => {});
    return () => controller.abort();
  }, []);

  async function create() {
    setWorking(true);
    setError("");
    try {
      const params = scenario ? `?scenario=${encodeURIComponent(scenario)}` : "";
      const result = await roomRequest(`/live/rooms${params}`);
      location.assign(`/?room=${result.room}`);
    } catch (error) {
      setError(error instanceof Error ? error.message : "Could not create a room.");
      setWorking(false);
    }
  }

  return (
    <section className="lobby">
      <div>
        <h1>Private poker</h1>
        <p className="muted">Create a table, then share the invite to play from separate browsers.</p>
      </div>
      <div className="panel lobby-panel">
        <label htmlFor="mode">Table format</label>
        <select id="mode" value={scenario} onChange={(event) => setScenario(event.target.value)}>
          <option value="">Standard table</option>
          <option value="side-pot">Side pot practice</option>
          {escrowAvailable && <option value="escrow">Testnet escrow table</option>}
        </select>
        <p className="muted">{scenario === "side-pot" ? "Three players, 10-chip buy-ins, and two fixed practice hands."
          : scenario === "escrow" ? "Use the funded testnet roster and sign a balance update after every hand."
            : "Up to six players, 500-chip buy-ins, and shuffled decks."}</p>
        <button className="primary" disabled={working} onClick={create}>{working ? "Creating room…" : "Create room"}</button>
        {error && <p className="error" role="alert">{error}</p>}
      </div>
    </section>
  );
}

function Room({ room }: { room: string }) {
  const { state, status, busy, error, send, retry } = useRoom(room);
  const [joinError, setJoinError] = useState("");
  const [joining, setJoining] = useState(false);
  const [copied, setCopied] = useState(false);
  const invite = useRef<HTMLInputElement>(null);
  const ready = status === "connected" && !busy && !joining && !state?.storagePaused;
  const connectionText = state?.storagePaused ? "Storage paused. Restart the server to recover this room." : {
    connecting: "Connecting…", connected: "Connected", reconnecting: "Connection lost. Reconnecting…",
    unavailable: "Room unavailable. Check the invite and confirm the Go server is running.",
  }[status];

  async function copyInvite() {
    try {
      await navigator.clipboard.writeText(location.href);
      setCopied(true);
    } catch {
      invite.current?.select();
    }
  }

  async function join(index: number) {
    setJoining(true);
    setJoinError("");
    try {
      const proof = state?.escrow ? { signature: await (await import("./escrow")).signSeat(state.escrow, index) } : undefined;
      await roomRequest(`/live/rooms/${room}/join?seat=${index + 1}`, proof);
      location.reload();
    } catch (error) {
      setJoinError(error instanceof Error ? error.message : "Could not join this seat.");
      setJoining(false);
    }
  }

  return (
    <>
      <section className="room-heading">
        <div>
          <h1>{state?.escrow ? "Escrow table" : state?.scenario === "side-pot" ? "Side pot practice" : "Private table"}</h1>
          <p className="muted" role="status">{connectionText}</p>
        </div>
        <div className="invite">
          <label htmlFor="invite">Invite link</label>
          <div>
            <input id="invite" ref={invite} readOnly value={location.href} onFocus={(event) => event.target.select()} />
            <button onClick={copyInvite}>{copied ? "Copied" : "Copy invite"}</button>
          </div>
        </div>
      </section>
      {(error || joinError) && <p className="error" role="alert">{error || joinError}</p>}
      {status === "unavailable" && <button onClick={retry}>Reconnect</button>}
      {!state && status === "unavailable" && <Suspense><OfflineCheckpoint room={room} /></Suspense>}
      {state && (
        <div className="layout">
          <Table state={state} canJoin={ready && state.canJoin} join={join} />
          <aside className="panel rail">
            <p className="eyebrow" id="identity">{state.viewer < 0 ? "Watching" : `You are Seat ${state.viewer + 1}`}</p>
            <h2 id="action-title">{state.actor < 0 ? streets[state.street] : state.actor === state.viewer ? "Your turn" : `Action on Seat ${state.actor + 1}`}</h2>
            {state.deadline && <Clock deadline={state.deadline} />}
            {state.bestHand && <div className="hand-strength">
              <span id="hand-strength-label">{state.street === "preflop" ? "Your starting hand" : "Your hand so far"}</span>
              <strong id="best-hand">{state.bestHand}</strong>
            </div>}
            {state.canJoin && <p className="muted">Choose an open seat to join.</p>}
            {state.street === "waiting" && state.viewer >= 0 && !state.canDeal && (
              <p className="muted">{state.escrow ? "Every seat must verify its assigned wallet before dealing."
                : state.scenario === "side-pot" ? "This practice hand starts with three players."
                  : state.viewer === 0 ? "Invite at least one more player to deal." : "The host will deal the first hand."}</p>
            )}
            <div className="actions">
              {state.canDeal && <button className="primary" disabled={!ready} onClick={() => send("deal")}>{state.hand ? "Deal next hand" : "Deal first hand"}</button>}
              {state.canRebuy && <button className="primary" disabled={!ready} onClick={() => send("rebuy")}>Rebuy · {state.buyIn} chips</button>}
              {state.actions.map((action) => <ActionButton key={`${action.kind}:${action.target}`} action={action} disabled={!ready} onClick={() => send("act", action)} />)}
            </div>
            <Payouts state={state} />
          </aside>
        </div>
      )}
      {state?.escrow && <Suspense fallback={<p className="muted">Loading escrow controls…</p>}>
        <EscrowPanel room={room} escrow={state.escrow} viewer={state.viewer} ready={ready} send={send} />
      </Suspense>}
    </>
  );
}

function Clock({ deadline }: { deadline: string }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(id);
  }, []);
  const seconds = Math.max(0, Math.ceil((Date.parse(deadline) - now) / 1000));
  return <p className={`clock ${seconds <= 10 ? "urgent" : ""}`} id="clock">{seconds}s remaining</p>;
}

function Table({ state, canJoin, join }: { state: TableState; canJoin: boolean; join: (index: number) => void }) {
  return (
    <section className="panel table-panel" aria-label="Poker table">
      <div className="table-header">
        <div><p className="eyebrow" id="hand">{state.hand ? `Hand ${state.hand}` : "Lobby"}</p><h2 id="street">{streets[state.street]}</h2></div>
        <p className="muted">{state.smallBlind} / {state.bigBlind} blinds · {state.buyIn} buy-in</p>
      </div>
      {state.scenario === "side-pot" && <p className="practice-note">The first two hands use fixed decks for the side pot walkthrough.</p>}
      <div className="table-stage">
        <div className="felt">
          <div className="pot">Pot <strong id="pot">{state.pot}</strong></div>
          <div className="board" aria-label="Community cards">
            {Array.from({ length: 5 }, (_, index) => <Card key={index} code={state.board[index]} />)}
          </div>
          {state.actor >= 0 && <div className="limits">
            <span>Current wager <strong>{state.current}</strong></span>
            <span>Street cap <strong>{state.ceiling || "—"}</strong></span>
          </div>}
        </div>
        <div className={`seats seats-${state.seats.length}`}>
          {state.seats.map((seat, index) => <SeatPanel key={index} seat={seat} index={index} state={state} join={canJoin ? () => join(index) : undefined} />)}
        </div>
      </div>
    </section>
  );
}

function SeatPanel({ seat, index, state, join }: { seat: Seat; index: number; state: TableState; join?: () => void }) {
  const flags = [
    state.button === index ? "D" : "",
    state.small === index ? "SB" : "",
    state.big === index ? "BB" : "",
    seat.allIn ? "All-in" : "",
  ].filter(Boolean);
  return (
    <section className={`seat ${state.actor === index ? "acting" : ""}`} aria-label={`Seat ${index + 1}`}>
      <div className="seat-heading">
        <strong>Seat {index + 1}{state.viewer === index ? " · You" : ""}</strong>
        {flags.length > 0 && <span className="flags">{flags.join(" · ")}</span>}
      </div>
      {seat.occupied ? <>
        <p className="seat-stack"><strong>{seat.stack}</strong> chips{seat.wager > 0 ? ` · ${seat.wager} in` : ""}</p>
        {seat.cards?.length ? <div className="hole-cards">{seat.cards.map((code) => <Card key={code} code={code} />)}</div>
          : seat.folded ? <p className="muted">Folded</p>
            : seat.inHand && state.street !== "settled" ? <div className="backs" aria-label="Two face-down cards"><span /><span /></div>
              : null}
      </> : join ? <button className="join-seat" onClick={join}>Join Seat {index + 1}</button> : <p className="muted">Open seat</p>}
    </section>
  );
}

function Card({ code }: { code?: string }) {
  if (!code) return <span className="card empty" aria-hidden="true">◇</span>;
  const suits: Record<string, { glyph: string; name: string }> = {
    c: { glyph: "♣", name: "clubs" }, d: { glyph: "♦", name: "diamonds" },
    h: { glyph: "♥", name: "hearts" }, s: { glyph: "♠", name: "spades" },
  };
  const rank = code[0] === "T" ? "10" : code[0];
  const suit = suits[code[1]];
  return <span className={`card ${"dh".includes(code[1]) ? "red" : ""}`} role="img" aria-label={`${rank} of ${suit.name}`}>
    <b aria-hidden="true">{rank}</b><span aria-hidden="true">{suit.glyph}</span>
  </span>;
}

function ActionButton({ action, disabled, onClick }: { action: Action; disabled: boolean; onClick: () => void }) {
  const labels = {
    check: "Check", fold: "Fold", call: `Call ${action.add}`, bet: `Bet ${action.to}`,
    raise: `Raise to ${action.to}`, "all-in": `All-in to ${action.to}`,
  };
  const detail = action.allIn && action.kind !== "all-in" ? "All-in"
    : action.kind === "raise" ? `Add ${action.add} chips` : "";
  return <button className={`action ${action.kind}`} disabled={disabled} onClick={onClick}>
    {labels[action.kind]}{detail && <small>{detail}</small>}
  </button>;
}

function Payouts({ state }: { state: TableState }) {
  if (state.street !== "settled") return null;
  return (
    <ul className="payouts" id="payouts">
      {state.pots?.map((pot, index) => (
        <li key={index}>{index === 0 ? "Main pot" : `Side pot ${index}`} · {pot.amount} chips → {pot.winners.map((seat) => `Seat ${seat + 1}`).join(", ")}</li>
      ))}
      {state.refunds?.map((refund, index) => refund > 0 && <li key={`refund-${index}`}>Seat {index + 1} gets {refund} uncalled {refund === 1 ? "chip" : "chips"} back</li>)}
    </ul>
  );
}

createRoot(document.getElementById("root")!).render(<StrictMode><App /></StrictMode>);
