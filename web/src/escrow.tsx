import { useEffect, useRef, useState } from "react";
import { createPublicClient, createWalletClient, custom, encodeFunctionData, formatEther, parseAbi, type EIP1193Provider } from "viem";
import { baseSepolia, foundry } from "viem/chains";
import type { Checkpoint, EscrowSession, EscrowState, SendCommand } from "./room";

type Bundle = { session: EscrowSession; seat: number; checkpoint: Checkpoint };
type ExitAction = "exit" | "challenge" | "withdraw";

const checkpointTypes = {
  Checkpoint: [
    { name: "tableId", type: "bytes32" },
    { name: "sessionId", type: "bytes32" },
    { name: "rulesHash", type: "bytes32" },
    { name: "sequence", type: "uint256" },
    { name: "balances", type: "uint256[]" },
    { name: "transcriptHash", type: "bytes32" },
  ],
} as const;
const bindingTypes = {
  SeatBinding: [
    { name: "tableId", type: "bytes32" },
    { name: "sessionId", type: "bytes32" },
    { name: "roomId", type: "bytes32" },
    { name: "seat", type: "uint256" },
  ],
} as const;
const abi = parseAbi([
  "function startExit()",
  "function startExitWithCheckpoint((uint256 sequence, uint256[] balances, bytes32 transcriptHash) checkpoint, bytes[] signatures)",
  "function challenge((uint256 sequence, uint256[] balances, bytes32 transcriptHash) checkpoint, bytes[] signatures)",
  "function withdraw(address recipient)",
]);

function storageKey(room: string) { return `poker-checkpoint:${room}`; }

export function readSavedBundle(room: string): Bundle | null {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey(room)) ?? "null") as Bundle | null;
    if (!saved?.session?.roster?.[saved.seat] || !Array.isArray(saved.checkpoint?.signatures)
      || saved.checkpoint.signatures.length !== saved.session.roster.length) return null;
    return saved;
  } catch {
    return null;
  }
}

function domain(session: EscrowSession) {
  return { name: "Poker Lab Escrow", version: "1", chainId: session.chainId, verifyingContract: session.contract };
}

async function seatWallet(session: EscrowSession, seat: number) {
  const provider = (window as Window & { ethereum?: EIP1193Provider }).ethereum;
  if (!provider) throw new Error("Open Poker Lab in a browser profile with an Ethereum wallet.");
  const chain = session.chainId === foundry.id ? foundry : session.chainId === baseSepolia.id ? baseSepolia : null;
  if (!chain) throw new Error("This escrow network is unsupported.");
  const transport = custom(provider);
  const wallet = createWalletClient({ chain, transport });
  const accounts = await wallet.requestAddresses();
  const account = session.roster[seat];
  if (!accounts.some((address) => address.toLowerCase() === account.toLowerCase())) {
    throw new Error("Select the wallet assigned to your seat.");
  }
  if (await wallet.getChainId() !== chain.id) throw new Error(`Switch your wallet to ${chain.name}.`);
  return { wallet, account, publicClient: createPublicClient({ chain, transport }) };
}

export async function signSeat(escrow: EscrowState, seat: number) {
  const { wallet, account } = await seatWallet(escrow, seat);
  return wallet.signTypedData({
    account, domain: domain(escrow), types: bindingTypes, primaryType: "SeatBinding",
    message: { tableId: escrow.tableId, sessionId: escrow.sessionId, roomId: escrow.roomId, seat: BigInt(seat) },
  });
}

function checkpointMessage(c: Checkpoint) {
  return { sequence: BigInt(c.sequence), balances: c.balances.map(BigInt), transcriptHash: c.transcriptHash };
}

function download(bundle: Bundle) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(bundle, null, 2)], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = `poker-checkpoint-${bundle.checkpoint.sequence}.json`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function ExitControls({ session, seat, bundle }: { session: EscrowSession; seat: number; bundle: Bundle | null }) {
  const [working, setWorking] = useState(false);
  const [message, setMessage] = useState("");
  async function submit(action: ExitAction) {
    setWorking(true);
    setMessage("");
    try {
      const { wallet, account, publicClient } = await seatWallet(session, seat);
      const signed = bundle?.checkpoint;
      const signatures = signed?.signatures.filter((signature) => signature !== "");
      const args = signed && signatures?.length === session.roster.length
        ? [checkpointMessage(signed), signatures] as const : null;
      if (action === "challenge" && !args) throw new Error("A complete signed checkpoint is required.");
      const data = action === "withdraw"
        ? encodeFunctionData({ abi, functionName: "withdraw", args: [account] })
        : action === "challenge" && args
          ? encodeFunctionData({ abi, functionName: "challenge", args })
          : action === "exit" && args
            ? encodeFunctionData({ abi, functionName: "startExitWithCheckpoint", args })
            : encodeFunctionData({ abi, functionName: "startExit" });
      const transaction = { account, to: session.contract, data };
      await publicClient.call(transaction);
      const hash = await wallet.sendTransaction(transaction);
      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      if (receipt.status !== "success") throw new Error("The transaction reverted.");
      setMessage(action === "withdraw" ? "Withdrawal confirmed." : action === "challenge" ? "Checkpoint submitted." : "Exit started. Withdraw after the challenge window.");
    } catch (error) {
      const message = (error as { shortMessage?: string }).shortMessage;
      setMessage(message ?? (error instanceof Error ? error.message : "The wallet request failed."));
    } finally {
      setWorking(false);
    }
  }
  return <>
    <div className="escrow-buttons">
      {bundle && <button onClick={() => download(bundle)}>Download checkpoint {bundle.checkpoint.sequence}</button>}
      <button disabled={working} onClick={() => submit("exit")}>{bundle ? "Start exit with saved checkpoint" : "Start exit from deposits"}</button>
      {bundle && <button disabled={working} onClick={() => submit("challenge")}>Challenge with saved checkpoint</button>}
      <button disabled={working} onClick={() => submit("withdraw")}>Withdraw after deadline</button>
    </div>
    {message && <p className="muted" role="status">{message}</p>}
  </>;
}

export function EscrowPanel({ room, escrow, viewer, ready, send }: {
  room: string; escrow: EscrowState; viewer: number; ready: boolean; send: SendCommand;
}) {
  const [bundle, setBundle] = useState(() => readSavedBundle(room));
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");
  const attempted = useRef(0);
  const latest = escrow.latest;

  useEffect(() => {
    if (viewer < 0 || !latest || !ready || attempted.current === latest.sequence) return;
    const { chainId, contract, tableId, sessionId, rulesHash, roster, buyInWei } = escrow;
    const saved: Bundle = {
      session: { chainId, contract, tableId, sessionId, rulesHash, roster, buyInWei }, seat: viewer, checkpoint: latest,
    };
    try {
      localStorage.setItem(storageKey(room), JSON.stringify(saved));
      setBundle(saved);
      if (escrow.saved[viewer] || send("save-checkpoint", undefined, { sequence: latest.sequence })) {
        attempted.current = latest.sequence;
      }
    } catch {
      setBundle(saved);
      setError("Browser storage is unavailable. Download the checkpoint and enable storage before the next deal.");
      attempted.current = latest.sequence;
    }
  }, [escrow, latest, ready, room, send, viewer]);

  async function sign(kind: "bind-wallet" | "sign-checkpoint") {
    setWorking(true);
    setError("");
    try {
      const pending = escrow.pending;
      let submitted: boolean;
      if (kind === "bind-wallet") {
        const signature = await signSeat(escrow, viewer);
        submitted = send(kind, undefined, { signature });
      } else if (pending) {
        const { wallet, account } = await seatWallet(escrow, viewer);
        const signature = await wallet.signTypedData({
          account, domain: domain(escrow), types: checkpointTypes, primaryType: "Checkpoint",
          message: { tableId: escrow.tableId, sessionId: escrow.sessionId, rulesHash: escrow.rulesHash, ...checkpointMessage(pending) },
        });
        submitted = send(kind, undefined, { signature, sequence: pending.sequence });
      } else {
        throw new Error("There is no checkpoint to sign.");
      }
      if (!submitted) throw new Error("Reconnect to the table before submitting your signature.");
    } catch (error) {
      setError(error instanceof Error ? error.message : "The wallet request failed.");
    } finally {
      setWorking(false);
    }
  }

  return <section className="panel escrow-panel" aria-label="Testnet escrow">
    <h2>Testnet escrow</h2>
    <p className="muted">{escrow.chainId === foundry.id ? "Local Anvil" : "Base Sepolia"} · {formatEther(BigInt(escrow.buyInWei))} ETH per seat</p>
    <p className="muted escrow-address">{escrow.contract}</p>
    {viewer >= 0 && <p className="muted escrow-address">Your assigned wallet · {escrow.roster[viewer]}</p>}
    <p className="muted">{escrow.bound.filter(Boolean).length} of {escrow.roster.length} wallets verified</p>
    {!escrow.connected && <p className="error">The server needs the escrow RPC configuration before it can deal.</p>}
    {viewer >= 0 && !escrow.bound[viewer] && <button disabled={!ready || working} onClick={() => sign("bind-wallet")}>Verify seat wallet</button>}
    {escrow.pending && <>
      <h3>Checkpoint {escrow.pending.sequence}</h3>
      <ul className="payouts">{escrow.pending.balances.map((amount, index) => (
        <li key={index}>Seat {index + 1} · {formatEther(BigInt(amount))} ETH · {escrow.pending?.signatures[index] ? "Signed" : "Awaiting signature"}</li>
      ))}</ul>
      {viewer >= 0 && !escrow.pending.signatures[viewer] && <button className="primary" disabled={!ready || working} onClick={() => sign("sign-checkpoint")}>Sign checkpoint {escrow.pending.sequence}</button>}
    </>}
    {latest && <p className="muted">Checkpoint {latest.sequence} signed. {escrow.saved.filter(Boolean).length} of {escrow.roster.length} seats saved a copy.</p>}
    {error && <p className="error" role="alert">{error}</p>}
    {viewer >= 0 && escrow.bound[viewer] && <ExitControls session={escrow} seat={viewer} bundle={bundle} />}
  </section>;
}

export function OfflineCheckpoint({ room }: { room: string }) {
  const bundle = readSavedBundle(room);
  if (!bundle) return null;
  return <section className="panel escrow-panel">
    <h2>Saved checkpoint {bundle.checkpoint.sequence}</h2>
    <ExitControls session={bundle.session} seat={bundle.seat} bundle={bundle} />
  </section>;
}
