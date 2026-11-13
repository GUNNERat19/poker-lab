const byId = (id) => document.getElementById(id);
let room = new URLSearchParams(location.search).get("room") ?? "";
let socket;
let state;
let pending;
let retry;
let connected = false;
let joining = false;

const notice = (message) => {
  byId("error").textContent = message;
  byId("error").hidden = !message;
};

async function post(path) {
  const response = await fetch(path, { method: "POST" });
  if (!response.ok) throw new Error((await response.text()).trim());
  return response.json();
}

function connect(reconnecting = false) {
  clearTimeout(retry);
  socket?.close();
  connected = false;
  pending = undefined;
  render();
  byId("room").hidden = false;
  byId("invite").value = location.href;
  byId("connection").textContent = reconnecting ? "Reconnecting…" : "Connecting…";

  const client = new WebSocket(`ws://${location.host}/live/rooms/${room}/socket`);
  socket = client;
  let receivedState = false;
  client.addEventListener("message", (event) => {
    if (client !== socket) return;
    const message = JSON.parse(event.data);
    if (message.type === "state") {
      receivedState = true;
      connected = true;
      byId("connection").textContent = message.storagePaused
        ? "Room storage is unavailable. Restart the server to recover this room."
        : "Connected";
      state = message;
    } else if (message.type === "ack" && message.id === pending?.id) {
      if (message.error) {
        notice(message.error);
        pending = undefined;
      } else {
        pending.acceptedVersion = message.version;
      }
    }
    if (pending?.acceptedVersion <= state?.version) pending = undefined;
    render();
  });
  client.addEventListener("close", () => {
    if (client !== socket) return;
    connected = false;
    pending = undefined;
    render();
    if (!receivedState && !reconnecting) {
      byId("connection").textContent = "Room unavailable. Check the invite or create a room.";
      return;
    }
    byId("connection").textContent = "Disconnected. Reconnecting…";
    retry = setTimeout(() => connect(true), 1000);
  });
}

byId("create").addEventListener("click", async () => {
  byId("create").disabled = true;
  try {
    const created = await post("/live/rooms");
    location.assign(`/live/?room=${created.room}`);
  } catch (error) {
    notice(error.message);
    byId("create").disabled = false;
  }
});

byId("copy").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(byId("invite").value);
    byId("copy").textContent = "Copied";
  } catch {
    byId("invite").select();
  }
});

async function join() {
  joining = true;
  notice("");
  render();
  try {
    await post(`/live/rooms/${room}/join`);
    connect();
  } catch (error) {
    notice(error.message);
  } finally {
    joining = false;
    render();
  }
}

function send(kind, action) {
  if (!connected || pending) return;
  notice("");
  pending = { id: crypto.randomUUID(), version: state.version, kind };
  if (action) {
    pending.action = action.kind;
    pending.target = action.target;
  }
  socket.send(JSON.stringify(pending));
  render();
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function card(code) {
  if (!code) return element("span", "card empty", "◇");
  const ranks = { T: "10" };
  const suits = { c: "♣", d: "♦", h: "♥", s: "♠" };
  const rank = ranks[code[0]] ?? code[0];
  const node = element("span", "card" + ("dh".includes(code[1]) ? " red" : ""));
  node.append(element("b", "", rank), element("span", "", suits[code[1]]));
  return node;
}

function seatPanel(seat, index) {
  const position = `Seat ${index + 1}`;
  const panel = element("section", "seat" + (state.actor === index ? " acting" : ""));
  panel.setAttribute("aria-label", position);
  const heading = element("div", "seat-title");
  heading.append(element("strong", "", position + (state.viewer === index ? " · You" : "")));
  const badges = element("div", "badges");
  for (const [visible, label] of [
    [state.button === index, "D"], [state.small === index, "SB"], [state.big === index, "BB"], [seat.allIn, "All-in"],
  ]) {
    if (visible) badges.append(element("span", "", label));
  }
  heading.append(badges);
  panel.append(heading);
  const wager = seat.wager ? ` · ${seat.wager} in` : "";
  panel.append(element("div", "seat-chips", seat.occupied ? `${seat.stack} chips${wager}` : "Open seat"));
  if (seat.cards?.length) {
    const cards = element("div", "hole-cards");
    cards.append(...seat.cards.map(card));
    panel.append(cards);
  } else if (seat.folded) {
    panel.append(element("div", "seat-status", "Folded"));
  } else if (seat.inHand && state.street !== "settled") {
    const backs = element("div", "backs");
    backs.setAttribute("aria-label", "Two face-down cards");
    backs.append(element("span"), element("span"));
    panel.append(backs);
  }
  return panel;
}

function control(label, className, callback, detail) {
  const button = element("button", className, label);
  button.type = "button";
  button.disabled = !connected || joining || Boolean(pending) || state.storagePaused;
  if (detail) button.append(element("small", "", detail));
  button.addEventListener("click", callback);
  return button;
}

function actionLabel(action) {
  switch (action.kind) {
    case "call": return `Call ${action.add}`;
    case "bet": return `Bet ${action.to}`;
    case "raise": return `Raise to ${action.to}`;
    case "all-in": return `All-in to ${action.to}`;
    default: return action.kind === "fold" ? "Fold" : "Check";
  }
}

function render() {
  if (!state) return;
  byId("blinds").textContent = `${state.smallBlind} / ${state.bigBlind}`;
  byId("buy-in").textContent = state.buyIn;
  const street = { waiting: "Waiting for players", preflop: "Preflop", flop: "Flop", turn: "Turn", river: "River", settled: "Hand complete" };
  byId("hand").textContent = state.hand ? `Hand ${state.hand}` : "Lobby";
  byId("street").textContent = street[state.street];
  byId("pot").textContent = state.pot;
  byId("current").textContent = state.current;
  byId("ceiling").textContent = state.ceiling || "—";
  byId("limits").hidden = state.actor < 0;
  byId("board").replaceChildren(...Array.from({ length: 5 }, (_, i) => card(state.board[i])));
  byId("seats").replaceChildren(...state.seats.map(seatPanel));
  byId("identity").textContent = state.viewer < 0 ? "Watching" : `You are Seat ${state.viewer + 1}`;
  byId("hand-strength").hidden = !state.bestHand;
  byId("hand-strength-label").textContent = state.street === "preflop" ? "Your starting hand" : "Your hand so far";
  byId("best-hand").textContent = state.bestHand ?? "";
  byId("action-title").textContent = state.actor < 0 ? street[state.street]
    : state.actor === state.viewer ? "Your turn" : `Action on Seat ${state.actor + 1}`;

  const controls = [];
  if (state.canJoin) controls.push(control("Join table", "primary", join));
  if (state.canDeal) controls.push(control(state.hand ? "Deal next hand" : "Deal first hand", "primary", () => send("deal")));
  if (state.canRebuy) controls.push(control(`Rebuy · ${state.buyIn} chips`, "secondary", () => send("rebuy")));
  for (const action of state.actions) {
    const detail = action.allIn && action.kind !== "all-in" ? "All-in"
      : action.kind === "raise" ? `Add ${action.add} chips` : "";
    controls.push(control(actionLabel(action), `action ${action.kind}`, () => send("act", action), detail));
  }
  byId("actions").replaceChildren(...controls);

  const payouts = (state.pots ?? []).map((pot, i) => {
    const winners = pot.winners.map((winner) => `Seat ${winner + 1}`).join(", ");
    return element("li", "", `${i === 0 ? "Main pot" : `Side pot ${i}`} · ${pot.amount} chips → ${winners}`);
  });
  (state.refunds ?? []).forEach((refund, index) => {
    if (refund) payouts.push(element("li", "", `Seat ${index + 1} gets ${refund} uncalled chips back`));
  });
  byId("payouts").replaceChildren(...payouts);
  updateClock();
}

function updateClock() {
  const remaining = state?.deadline ? Math.max(0, Math.ceil((Date.parse(state.deadline) - Date.now()) / 1000)) : null;
  byId("clock").textContent = remaining === null ? "" : `${remaining}s remaining`;
}

if (/^[A-Za-z0-9_-]{12}$/.test(room)) {
  connect();
} else if (room) {
  notice("The invite link is invalid.");
}
setInterval(updateClock, 250);
