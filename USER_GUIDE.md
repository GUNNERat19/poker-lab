# Poker Lab local user guide

Poker Lab runs the Go Hold’em engine in your browser. Use solo practice to control every seat and replay a fixed example, or use private multiplayer to play from separate browser sessions. Install Go 1.24 or later to run it.

## Start the table

Open Terminal in the project folder and run:

```sh
go run ./cmd/poker-demo
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). The **Raise cap** scenario opens with its first hand already dealt. Keep the Terminal window running and press **Ctrl+C** when you're done. The demo runs on your computer with play chips. It uses in-memory state and resets when you restart the server.

If port 8080 is busy, run `go run ./cmd/poker-demo -port 8081` and open [http://127.0.0.1:8081](http://127.0.0.1:8081). If Terminal cannot find Go, install it and reopen Terminal. [Go installation guide](https://go.dev/doc/install)

## Play a private hand from two browsers

1. Open [Private multiplayer](http://127.0.0.1:8080/live/) and click **Create room**. You are Seat 1, the host.
2. Copy the invite link and open it in a private window or another browser on the same computer. Click **Join table** to take Seat 2. Tabs in the same browser profile share the seat.
3. In the host window, click **Deal first hand**. Each player sees their own cards. Refresh Seat 1 while its first turn is active. The seat and cards stay the same, and the countdown continues from the original deadline.
4. Seat 1 clicks **Call 3**, then Seat 2 clicks **Check**. On the flop, turn, and river, Seat 2 checks first and Seat 1 checks second. Both windows show the 10-chip pot and the showdown result.
5. The host can click **Deal next hand** after settlement. A busted player can **Rebuy** between hands. Visitors can watch from the invite link and take an open seat between hands.

Each turn starts with 60 seconds. If time expires, the server checks when possible and folds otherwise. To try a shorter clock in a new room, start the demo with `go run ./cmd/poker-demo -turn-timeout 10s`. The first run may download Go dependencies.

Each browser profile keeps its seat through a private cookie for that room. The invite identifies the room and lets a new profile join an open seat. Refresh or reopen the invite in the same profile to return to your seat while the room is running.

**Your starting hand** describes your two cards before the flop. **Your hand so far** updates as community cards appear. It shows the strongest five-card combination you can make with your cards and the current board, even while another player is acting. The label is visible to your seat only.

## Run the React lobby and table

Keep `go run ./cmd/poker-demo` running in one Terminal window. In a second window, use Node.js 22.12 or later and run these commands from the project folder.

```sh
npm --prefix web ci
npm --prefix web run dev
```

Open [http://127.0.0.1:5173](http://127.0.0.1:5173). The **Standard table** uses 500-chip buy-ins and shuffled decks. The host creates a room and shares the invite. Guests open it in separate browser profiles and choose an open seat. If you start Go on another port, set `POKER_BACKEND_URL` to that local address when starting the React client.

### Three-player side pot and rebuy walkthrough

Choose **Side pot practice** and click **Create room**. Open the invite in two additional browser profiles and select **Join Seat 2** and **Join Seat 3**. Two windows in the same private browsing session share a seat, so use three separate profiles or browsers.

| Step | Actions in order | Expected result |
| --- | --- | --- |
| Hand 1, preflop | Seat 1 deals. Seat 1 **Call 5**, Seat 2 **Call 3**, Seat 3 **Check**. | All three seats see the flop. |
| Hand 1, flop | Seat 2 **Check**, Seat 3 **Bet 5**, Seat 1 **Call 5**, Seat 2 **Fold**. | The board runs out. Seats 1 and 3 split the pot. Stacks are **12 / 5 / 13**. |
| Hand 2, preflop | Seat 1 deals again. Refresh Seat 2 while it has the first turn. Then Seat 2 **Call 5**, Seat 3 **Raise to 10**, Seat 1 **Call 5**. | Seat 2 keeps its pocket aces and the original turn deadline on refresh. Seat 2 is all-in after calling. |
| Hand 2, flop | Seat 3 **All-in to 3**, Seat 1 **Call 2**. | Seat 2 wins the **15-chip main pot**, Seat 3 wins the **14-chip side pot** and gets **1 uncalled chip** back. Seat 1 busts. |
| Rebuy | Seat 1 clicks **Rebuy · 10 chips**, then **Deal next hand**. | Seat 1 returns with 10 chips before the blinds. Hand 3 uses a shuffled deck. |

## Keep multiplayer rooms after a restart

The default command runs in memory. To save rooms, start with a dedicated PostgreSQL database. For a local instance using a Unix socket in `/tmp`, run the following from the project folder.

```sh
createdb -h /tmp poker_lab
if [ ! -e "$HOME/.poker-lab-storage-key" ]; then
  (umask 077; set -C; openssl rand -base64 32 > "$HOME/.poker-lab-storage-key")
fi
export POKER_DATABASE_URL='host=/tmp dbname=poker_lab'
export POKER_STORAGE_KEY="$(cat "$HOME/.poker-lab-storage-key")"
go run ./cmd/poker-demo
```

Create the database and key once. If you already have a dedicated database, use its connection string in `POKER_DATABASE_URL`. The app creates the `poker_lab` schema on first startup. Keep using the same key to open saved rooms. It encrypts the stored events that contain unrevealed cards. Run one Poker Lab server per database.

To test recovery, create a multiplayer room with PostgreSQL enabled and deal a hand. Stop and restart the app with the same database and key, then reopen the invite in the same browser profiles. The seats, cards, stacks, and pot resume. The original deadline still applies, so an expired turn is resolved when the room comes back. Solo practice starts fresh on every server launch.

## Read the table

- The highlighted seat acts next. Use the buttons under **Action on** for that seat. Expand **Peek at hole cards** to see its cards.
- **D**, **SB**, and **BB** mark the dealer, small blind, and big blind. **Current wager** is the amount each active seat must match on this street. **Street cap** is the maximum it can commit on that street.
- **Call 140** means add 140 chips now. **Raise to 160** means bring that seat's total on this street to 160. The button shows how many chips to add.
- **Hand history** shows the accepted actions with the newest first. Once the hand settles, **Payouts** shows who won each pot and any uncalled chips returned.
- Select a scenario and click **Start scenario** to reset the table. The cap and rebuy scenarios use fixed decks for their first hands. **Deal next hand** uses a fresh shuffle and keeps stacks from the prior hand.

## Sample hand 1 — hit the raise cap

Select **Raise cap** and click **Start scenario**. The blinds are 2/5 and each of the four seats starts with 500 chips.

| Step | Click these actions in order | What to look for |
| --- | --- | --- |
| Preflop | Seat 4 **Call 5**, Seat 1 **Call 5**, Seat 2 **Call 3**, Seat 3 **Check** | The flop starts with a 20-chip pot. |
| Open the flop | Seat 2 **Bet 20** | The cap locks at 160. Seat 3 can raise to 40, 80, or 160. |
| Raise twice | Seat 3 **Raise to 80**, Seat 4 **Raise to 160** | The cap stays at 160. Seat 1 gets **Fold** and **Call 160**. |
| Close the flop | Seat 1 **Fold**, Seat 2 **Call 140**, Seat 3 **Call 80** | The turn starts with a 500-chip pot and a fresh street cap. |
| Finish the hand | **Check** from Seats 2, 3, and 4 on the turn, then repeat on the river. | Seat 4 wins the 500-chip pot with a pair of aces. |

## Sample hand 2 — bust and rebuy

1. Select **Bust and rebuy** and click **Start scenario**. Both seats start with five chips. Seat 2 has posted all five as the big blind.
2. From Seat 1, click **Call 3**. The rest of the board is dealt automatically. Seat 2 wins the 10-chip pot and Seat 1 has zero chips.
3. Click **Rebuy Seat 1 · 5 chips**, then **Deal next hand**. The button moves to Seat 2, which acts first preflop.

For open-ended practice, select **Shuffled table**. All seats start with 500 chips and the deck is shuffled for each hand.

## Try escrow on a local chain

The escrow prototype runs separately from the poker table. It uses local Anvil ETH and sample balances to exercise funding, signed checkpoints, and withdrawals. Install Foundry and use the Node version listed in the React setup above.

From the project folder, install dependencies and run the contract tests. The test command also builds the contract artifact used by the demo.

```sh
npm --prefix escrow ci
forge test --root escrow
```

In one Terminal window, start Anvil with `anvil --host 127.0.0.1`. In a second window, run `npm --prefix escrow run demo`. The demo deploys two sessions to that local chain using the default Anvil development accounts.

- In the first session, one player signs a proposed balance update. The contract rejects the incomplete checkpoint. A player starts exit and both withdraw their original 1 ETH deposits after the challenge window.
- In the second session, one player starts exit with signed sequence 1. A relay submits signed sequence 2 before the deadline. The players withdraw 1.5 and 0.5 ETH from that newer checkpoint.

A checkpoint is a signed snapshot of all player balances in seat order. It binds the table, session, rules version, sequence, balances, and transcript hash to the chain and contract. The demo compares the wallet and contract hashes before signing. If a seat never funds, depositors can withdraw once the funding window expires.

If a player withholds their signature after a hand, the exit pays the latest fully signed checkpoint submitted before the deadline, or the original deposits if none was submitted. The table workflow below gives each player a copy to submit during an exit.

## Play an escrow table with two wallets

For a local run, start Anvil and build the escrow contract as above. Run `npm --prefix escrow run demo -- --fund-only`. It funds a fresh session using Anvil's first two development wallets and prints the contract address. Set `POKER_ESCROW_ADDRESS` to that address and `POKER_ESCROW_RPC_URL` to `http://127.0.0.1:8545` before starting the Go server.

1. Open the React lobby and create a **Testnet escrow table**. Use two browser profiles with separate Ethereum accounts on the configured network. The first wallet in the contract roster belongs to Seat 1 and the second to Seat 2. The prototype verifies signatures from standard accounts with private keys.
2. The host clicks **Verify seat wallet**. The guest opens the invite and signs when joining the open seat. The host can deal once all seats are verified and the escrow is funded.
3. At the end of a hand, review the proposed ETH balances and click **Sign checkpoint** in each profile. Both profiles save the complete signed checkpoint before the host can deal again. **Download checkpoint** saves a portable JSON copy.
4. Use **Start exit** to open the contract's challenge window. **Challenge with saved checkpoint** submits a newer signed version during that window. After it closes, click **Withdraw after deadline**. The wallet confirms each on-chain transaction.

An escrow room uses 500 chips per seat and maps them to the deposited ETH amount exactly. A busted player sits out but still signs checkpoints because the roster is fixed. Adding funds requires a new escrow session. Ordinary play-chip tables continue to offer rebuys.

### Repeat on Base Sepolia

Use a PostgreSQL-backed server and two wallets funded with Base Sepolia test ETH. The example below assumes Foundry wallet aliases `poker-alice` and `poker-bob`, and the two corresponding public addresses already set as `WALLET_1` and `WALLET_2`. Base Sepolia uses chain ID 84532. [Base network details](https://docs.base.org/get-started/connect-to-base)

```sh
export POKER_ESCROW_RPC_URL=https://sepolia.base.org
cast chain-id --rpc-url "$POKER_ESCROW_RPC_URL"
forge create --root escrow src/PokerEscrow.sol:PokerEscrow \
  --rpc-url "$POKER_ESCROW_RPC_URL" --account poker-alice --broadcast \
  --constructor-args "$(cast keccak poker-lab-testnet)" "$(cast keccak "$(openssl rand -hex 16)")" \
  "$(cast keccak capped-holdem-v1)" "[$WALLET_1,$WALLET_2]" 500000000000000 1800 180
```

Set `POKER_ESCROW_ADDRESS` to the displayed deployment address, then fund it once from each wallet. Start Go with the PostgreSQL and escrow environment variables set, and open the React lobby.

```sh
cast send "$POKER_ESCROW_ADDRESS" 'deposit()' --value 500000000000000 \
  --rpc-url "$POKER_ESCROW_RPC_URL" --account poker-alice
cast send "$POKER_ESCROW_ADDRESS" 'deposit()' --value 500000000000000 \
  --rpc-url "$POKER_ESCROW_RPC_URL" --account poker-bob
```

- **Missing signer.** Finish the first hand and have one player leave the checkpoint unsigned. Use **Start exit from deposits**. After the challenge window, both players can withdraw their deposits.
- **Stale checkpoint.** Use a fresh funded contract and room. Save and download checkpoint 1, then play and sign checkpoint 2. Use the command below to open exit with checkpoint 1. The other player uses **Challenge with saved checkpoint** to submit checkpoint 2 before the deadline. Withdraw after the window closes.

```sh
file=poker-checkpoint-1.json
checkpoint=$(jq -r '.checkpoint | "(\(.sequence),[\(.balances | join(","))],\(.transcriptHash))"' "$file")
signatures=$(jq -r '.checkpoint.signatures | "[" + join(",") + "]"' "$file")
cast send "$POKER_ESCROW_ADDRESS" 'startExitWithCheckpoint((uint256,uint256[],bytes32),bytes[])' \
  "$checkpoint" "$signatures" --rpc-url "$POKER_ESCROW_RPC_URL" --account poker-alice
```

## Try the private-dealing prototype

Run from the project folder:

```sh
go run ./cmd/poker-private-deal -seats 3
go run ./cmd/poker-private-deal -seats 6
go run ./cmd/poker-private-deal -withhold shuffle
go run ./cmd/poker-private-deal -withhold reveal
```

The first two commands simulate a 52-card deal and report proof size and elapsed time. Each simulated player shuffles the encrypted deck and submits a proof. Hole cards open for their owners, community cards open for everyone, and the simulated showdown opens the two remaining hands. The last two commands simulate a participant withholding a shuffle or a board share and show where the deal aborts. The simulation runs inside one process. The multiplayer table uses its existing software dealer.
