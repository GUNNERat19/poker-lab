import { useEffect, useRef, useState } from "react";
import type { Address, Hex } from "viem";

export type Street = "waiting" | "preflop" | "flop" | "turn" | "river" | "settled";

export type Action = {
  kind: "check" | "fold" | "call" | "bet" | "raise" | "all-in";
  target: number;
  add: number;
  to: number;
  allIn: boolean;
};

export type Seat = {
  occupied: boolean;
  stack: number;
  wager: number;
  allIn: boolean;
  folded: boolean;
  inHand: boolean;
  cards?: string[];
};

export type Checkpoint = {
  sequence: number;
  balances: string[];
  transcriptHash: Hex;
  signatures: (Hex | "")[];
};

export type EscrowSession = {
  chainId: number;
  contract: Address;
  tableId: Hex;
  sessionId: Hex;
  rulesHash: Hex;
  roster: Address[];
  buyInWei: string;
};

export type EscrowState = EscrowSession & {
  roomId: Hex;
  bound: boolean[];
  saved: boolean[];
  pending?: Checkpoint;
  latest?: Checkpoint;
  connected: boolean;
};

export type TableState = {
  type: "state";
  version: number;
  hand: number;
  street: Street;
  viewer: number;
  actor: number;
  button: number;
  small: number;
  big: number;
  smallBlind: number;
  bigBlind: number;
  current: number;
  ceiling: number;
  pot: number;
  buyIn: number;
  scenario?: "side-pot" | "escrow";
  deadline?: string;
  canDeal: boolean;
  canJoin: boolean;
  canRebuy: boolean;
  board: string[];
  bestHand?: string;
  seats: Seat[];
  actions: Action[];
  pots?: { amount: number; winners: number[] }[];
  refunds?: number[];
  storagePaused?: boolean;
  escrow?: EscrowState;
};

type Ack = { type: "ack"; id: string; version: number; error?: string };
type Command = {
  id: string;
  version: number;
  kind: "act" | "deal" | "rebuy" | "bind-wallet" | "sign-checkpoint" | "save-checkpoint";
  action?: Action["kind"];
  target?: number;
  sequence?: number;
  signature?: Hex;
};
export type SendCommand = (kind: Command["kind"], action?: Action, checkpoint?: Pick<Command, "sequence" | "signature">) => boolean;
type Pending = { command: Command; acceptedVersion?: number };

export const roomPattern = /^[A-Za-z0-9_-]{12}$/;

export async function roomRequest(path: string, proof?: { signature: Hex }): Promise<{ room: string; seat: number }> {
  const response = await fetch(path, {
    method: "POST",
    ...(proof && { headers: { "Content-Type": "application/json" }, body: JSON.stringify(proof) }),
  });
  if (!response.ok) {
    const reason = response.headers.get("content-type")?.startsWith("text/plain")
      ? (await response.text()).trim()
      : "";
    throw new Error(reason || "Could not reach the poker server.");
  }
  return response.json();
}

export function useRoom(room: string) {
  const [state, setState] = useState<TableState>();
  const [status, setStatus] = useState<"connecting" | "connected" | "reconnecting" | "unavailable">("connecting");
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const socket = useRef<WebSocket | null>(null);
  const latest = useRef<TableState | undefined>(undefined);
  const pending = useRef<Pending | undefined>(undefined);

  useEffect(() => {
    let stopped = false;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let delay = 750;

    function connect() {
      const url = new URL(`/live/rooms/${room}/socket`, window.location.href);
      url.protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const client = new WebSocket(url);
      socket.current = client;
      let firstState = true;

      client.onmessage = ({ data }) => {
        if (stopped || socket.current !== client) return;
        const message = JSON.parse(data) as TableState | Ack;
        if (message.type === "state") {
          latest.current = message;
          setState(message);
          setStatus("connected");
          delay = 750;
          if (firstState && pending.current) {
            const receipt = pending.current.acceptedVersion;
            if (receipt === undefined || receipt > message.version) {
              client.send(JSON.stringify(pending.current.command));
            }
          }
          firstState = false;
        } else if (message.id === pending.current?.command.id) {
          if (message.error) {
            setError(message.error);
            pending.current = undefined;
            setBusy(false);
          } else {
            pending.current.acceptedVersion = message.version;
          }
        }
        const accepted = pending.current?.acceptedVersion;
        if (accepted !== undefined && latest.current && latest.current.version >= accepted) {
          pending.current = undefined;
          setBusy(false);
        }
      };
      client.onclose = () => {
        if (stopped || socket.current !== client) return;
        if (!latest.current) {
          setStatus("unavailable");
          return;
        }
        setStatus("reconnecting");
        retry = setTimeout(connect, delay);
        delay = Math.min(delay * 2, 5000);
      };
    }

    connect();
    return () => {
      stopped = true;
      clearTimeout(retry);
      socket.current?.close();
      socket.current = null;
    };
  }, [room, attempt]);

  const send: SendCommand = (kind, action, checkpoint) => {
    const client = socket.current;
    const current = latest.current;
    if (pending.current || !current || current.storagePaused || client?.readyState !== WebSocket.OPEN) return false;
    const command: Command = {
      id: crypto.randomUUID(),
      version: current.version,
      kind,
      ...(action && { action: action.kind, target: action.target }),
      ...checkpoint,
    };
    setError("");
    pending.current = { command };
    setBusy(true);
    client.send(JSON.stringify(command));
    return true;
  };

  function retry() {
    setStatus("connecting");
    setAttempt((previous) => previous + 1);
  }

  return { state, status, busy, error, send, retry };
}
