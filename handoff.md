# Poker implementation handoff

Read this file before starting work. Use [DESIGN.md](DESIGN.md) for the agreed rules and architecture. After each milestone, record what landed, the checks you ran, and the first unfinished step here.

## Current progress

- The agreed backend is Go. The browser will use React and TypeScript, with PostgreSQL for persistence and Solidity for the later settlement experiment.
- The first milestone is complete. The [Go rules engine](poker/table.go) supports 2–6 seats, button rotation, blinds, the fixed street ceiling, short all-ins, side pots, showdowns, refunds, and zero-stack rebuys between hands.
- The engine accepts a complete deck as input. Use `ShuffleDeck()` for the play-chip dealer and a fixed deck for deterministic tests. The coordinator owns seat authentication, timing, and storage. Use `View(authenticatedSeat)` for a player and `View(poker.NoSeat)` for an observer.
- Poker Lab is a local browser frontend for manual testing. Run `go run ./cmd/poker-demo` and open [http://127.0.0.1:8080](http://127.0.0.1:8080). Follow the [local user guide](USER_GUIDE.md) for repeatable raise cap and rebuy walkthroughs. The Go server embeds the HTML and CSS and holds one practice table in memory. One browser controls all seats.
- Multiplayer runs on the Go server. The React lobby and table are in `web/`. With Go running, install with `npm --prefix web ci` and start with `npm --prefix web run dev`. Open [http://127.0.0.1:5173](http://127.0.0.1:5173). The Go server also serves its original [multiplayer page](http://127.0.0.1:8080/live/).
- The React client uses the existing room credentials and WebSocket protocol. Guests choose an open seat. The **Side pot practice** room uses 10-chip stacks and fixed decks for the first two hands. The [three-player walkthrough](USER_GUIDE.md#three-player-side-pot-and-rebuy-walkthrough) covers a refresh, separate main and side pot winners, an uncalled-chip refund, a rebuy, and the next deal.
- The Go coordinator in `internal/live` owns one command loop and turn timer per room. Room-scoped cookies recover seats on refresh. Each WebSocket receives only its seat's allowed view. Commands carry a state version and an action ID. Each seat retains the last 128 accepted command receipts.
- Each active player sees a private hand description in multiplayer. Before the flop it describes their starting cards. From the flop onward it uses the same five-card ranking as showdown and considers only their hole cards and the visible board.
- Multiplayer supports PostgreSQL when `POKER_DATABASE_URL` and `POKER_STORAGE_KEY` are set. One transaction writes each encrypted room event and its chip journal rows before broadcasting. On startup the coordinator replays the events to restore seats, decks, stacks, deadlines, and action receipts. Seat credentials are stored as hashes. The [user guide](USER_GUIDE.md) covers setup and a manual restart test.
- The demo binds to localhost. The default mode keeps rooms in memory. PostgreSQL mode supports one server per database and replays the full history on startup. If a database write fails, the affected room pauses and a restart reloads its committed state.
- The standalone escrow prototype lives in `escrow/`. Each contract funds one fixed roster of 2–6 players with equal native ETH deposits. An incomplete roster can refund deposits after the funding deadline. A player can start exit with or without a signed checkpoint. During the challenge window, anyone can submit a newer checkpoint signed by every seat. The original deadline stays fixed.
- The React escrow table binds browser seats to the configured contract's wallet roster. It checks the funding phase before each deal and proposes EIP-712 checkpoints from settled hand balances. Each browser saves the full signed bundle locally and confirms receipt before the next deal. PostgreSQL preserves signature and receipt events. Players can download the bundle and use their wallets to start exits, challenge stale checkpoints, and withdraw.
- Escrow rooms support local Anvil or Base Sepolia and require a fresh contract to add chips. Standard rooms continue to use play chips. A hand with missing signatures can be left out of settlement. The latest fully signed checkpoint submitted before the deadline wins, with original deposits as the fallback.

## Verification

Run these commands from the project root with Go 1.24 or later.

```sh
go test -race ./...
go vet ./...
go test -run '^$' -fuzz '^FuzzHandConservesChips$' -fuzztime=12s -parallel=4 ./poker
```

All three checks passed. The fuzz run exercised 188,440 generated sequences of hands and checked chip conservation, duplicate cards, and hand completion. The scenario tests include the 20 → 80 → 160 cap, short blind and all-in handling, pot splits, card visibility, and rebuys.

The demo's HTTP tests cover both guide walkthroughs, rejection of stale submissions and over-cap raises, and card visibility. Both walkthroughs also passed in headless Chrome, including the payout, rebuy, and next deal. The desktop and mobile layouts were checked.

For multiplayer, `go test -race ./...` and `go vet ./...` passed. Chrome played a full hand from two independent seat sessions, refreshed Seat 1 mid-hand, and verified that the original cards and deadline were retained. A third connection watched without seeing private cards before showdown. Coordinator tests cover out-of-turn actions, duplicate receipts, stale versions, and timeout folds.

The hand description tests cover preflop, flop, turn, river, and playing the board. The Chrome walkthrough verified the private labels, refresh, and that observer messages omit the description.

PostgreSQL 15 integration passed with a dedicated temporary database. The test checks room recovery, original deadline, duplicate action receipts, wrong-key rejection, chip journal totals, and rollback if a journal entry fails. A Chrome walkthrough also killed the server mid-hand, restarted it, recovered both seats, cards, and countdown, and played through to showdown. To rerun the Go integration test, set `POKER_TEST_DATABASE_URL` to a dedicated test database and run `go test -race -run TestPostgresRoomRecovery ./internal/live`.

The React build and Go race tests passed. A walkthrough of the built client in three isolated Chrome profiles verified seat selection, the original cards and countdown after refresh, the 15-chip main pot to Seat 2, the 14-chip side pot and 1-chip refund to Seat 3, and Seat 1's 10-chip rebuy followed by hand three. The payout screen was checked at desktop and mobile widths.

The escrow's nine Foundry tests passed, including 256 fuzz cases for balance conservation. The tests cover funding refunds, unsigned hands, stale checkpoints, signature domain binding, and withdrawal retries. The viem demo on Anvil matched the contract's EIP-712 digest and completed both missing-signer and stale-checkpoint withdrawals.

The integrated React, Go, and Anvil walkthrough used two browser profiles and Anvil test wallets. It verified wallet binding, a blocked next deal while a signature was missing, a fully signed bundle saved in both profiles, a stale exit replaced by checkpoint 2, and both withdrawals. The PostgreSQL recovery test replayed the signatures and save receipts. Base Sepolia setup is in the [user guide](USER_GUIDE.md#repeat-on-base-sepolia). The public run still needs funded testnet wallets.

## Next steps, in order

1. Run the integrated missing-signer and stale-checkpoint scenarios on Base Sepolia with funded testnet wallets.
2. Prototype a verifiable private deal and measure what happens when a participant withholds a shuffle or reveal. Resolve how to handle an unsigned result before using escrow with real stakes.
