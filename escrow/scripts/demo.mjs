import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";

import { createPublicClient, createTestClient, createWalletClient, hashTypedData, http, keccak256, parseEther, toHex } from "viem";
import { mnemonicToAccount } from "viem/accounts";
import { foundry } from "viem/chains";

const port = Number(process.env.POKER_ANVIL_PORT ?? 8545);
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error("POKER_ANVIL_PORT must be a valid local port.");
}

const artifact = JSON.parse(await readFile(new URL("../out/PokerEscrow.sol/PokerEscrow.json", import.meta.url), "utf8"));
const transport = http(`http://127.0.0.1:${port}`);
const chain = foundry;
const publicClient = createPublicClient({ chain, transport });
const testClient = createTestClient({ chain, transport, mode: "anvil" });
const chainId = await publicClient.getChainId();
assert.equal(chainId, chain.id, "Run this demo on the default local Anvil chain.");

// These publicly known development keys belong to the default Anvil accounts.
const mnemonic = "test test test test test test test test test test test junk";
const accounts = Array.from({ length: 4 }, (_, addressIndex) => mnemonicToAccount(mnemonic, { addressIndex }));
const wallets = accounts.map((account) => createWalletClient({ account, chain, transport }));
const players = accounts.slice(0, 2);
const recipients = accounts.slice(2).map((account) => account.address);
const buyIn = parseEther("1");

const types = {
  Checkpoint: [
    { name: "tableId", type: "bytes32" },
    { name: "sessionId", type: "bytes32" },
    { name: "rulesHash", type: "bytes32" },
    { name: "sequence", type: "uint256" },
    { name: "balances", type: "uint256[]" },
    { name: "transcriptHash", type: "bytes32" },
  ],
};

async function receipt(hash) {
  const result = await publicClient.waitForTransactionReceipt({ hash });
  assert.equal(result.status, "success", "The transaction reverted.");
  return result;
}

async function openSession(label) {
  const ids = {
    tableId: keccak256(toHex("local-table")),
    sessionId: keccak256(toHex(label)),
    rulesHash: keccak256(toHex("capped-holdem-v1")),
  };
  const deployment = await wallets[0].deployContract({
    abi: artifact.abi,
    bytecode: artifact.bytecode.object,
    args: [ids.tableId, ids.sessionId, ids.rulesHash, players.map(({ address }) => address), buyIn, 600n, 120n],
  });
  const { contractAddress: address } = await receipt(deployment);
  assert.ok(address);
  const contract = { abi: artifact.abi, address };
  for (const wallet of wallets.slice(0, 2)) {
    await receipt(await wallet.writeContract({ ...contract, functionName: "deposit", value: buyIn }));
  }
  return { contract, ids };
}

async function signedState(session, sequence, balances, signers = players) {
  const checkpoint = { sequence, balances, transcriptHash: keccak256(toHex(`sample hand ${sequence}`)) };
  const typedData = {
    domain: { name: "Poker Lab Escrow", version: "1", chainId, verifyingContract: session.contract.address },
    primaryType: "Checkpoint",
    types,
    message: { ...session.ids, ...checkpoint },
  };
  const digest = await publicClient.readContract({ ...session.contract, functionName: "checkpointDigest", args: [checkpoint] });
  assert.equal(hashTypedData(typedData), digest, "Wallet and contract computed different checkpoint digests.");
  return [checkpoint, await Promise.all(signers.map((signer) => signer.signTypedData(typedData)))];
}

async function withdrawAfterWindow({ contract }, balances) {
  const deadline = await publicClient.readContract({ ...contract, functionName: "exitDeadline" });
  await testClient.setNextBlockTimestamp({ timestamp: deadline });
  await testClient.mine({ blocks: 1 });
  for (const [i, wallet] of wallets.slice(0, 2).entries()) {
    const recipient = recipients[i];
    const before = await publicClient.getBalance({ address: recipient });
    await receipt(await wallet.writeContract({ ...contract, functionName: "withdraw", args: [recipient] }));
    assert.equal((await publicClient.getBalance({ address: recipient })) - before, balances[i]);
  }
  assert.equal(await publicClient.getBalance({ address: contract.address }), 0n);
}

if (process.argv.includes("--fund-only")) {
  const session = await openSession(randomUUID());
  console.log(`POKER_ESCROW_ADDRESS=${session.contract.address}`);
} else {
  const unsigned = await openSession("missing-signer");
  const incomplete = await signedState(unsigned, 1n, [parseEther("1.5"), parseEther("0.5")], [players[0]]);
  await assert.rejects(
    publicClient.simulateContract({
      ...unsigned.contract, functionName: "startExitWithCheckpoint", args: incomplete, account: players[0].address,
    }),
    /InvalidCheckpoint/,
  );
  await receipt(await wallets[0].writeContract({ ...unsigned.contract, functionName: "startExit" }));
  await withdrawAfterWindow(unsigned, [buyIn, buyIn]);
  console.log("Missing signature: both players withdrew their original deposits.");

  const challenged = await openSession("stale-checkpoint");
  const stale = await signedState(challenged, 1n, [parseEther("0.75"), parseEther("1.25")]);
  const latestBalances = [parseEther("1.5"), parseEther("0.5")];
  const latest = await signedState(challenged, 2n, latestBalances);
  await receipt(await wallets[1].writeContract({
    ...challenged.contract, functionName: "startExitWithCheckpoint", args: stale,
  }));
  await receipt(await wallets[2].writeContract({ ...challenged.contract, functionName: "challenge", args: latest }));
  assert.equal(await publicClient.readContract({ ...challenged.contract, functionName: "sequence" }), 2n);
  await withdrawAfterWindow(challenged, latestBalances);
  console.log("Stale checkpoint: a relayer submitted sequence 2 and the players withdrew 1.5 and 0.5 local ETH.");
}
