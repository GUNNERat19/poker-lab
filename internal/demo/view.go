package demo

import (
	"fmt"
	"slices"
	"strings"

	"cappedpoker/poker"
)

type playingCard struct {
	Rank, Suit, Label string
	Red               bool
}

type seatPanel struct {
	Number        int
	Stack, Wager  poker.Chips
	Badges        []string
	Cards         []playingCard
	Folded, Live  bool
	Acting, Rebuy bool
}

type button struct {
	Label, Detail string
	Kind          poker.ActionKind
	Target        poker.Chips
}

type scenarioChoice struct {
	ID, Name string
	Selected bool
}

type page struct {
	Version            uint64
	Hand               uint64
	Street, Mode, Hint string
	Small, Big, BuyIn  poker.Chips
	Current, Ceiling   poker.Chips
	Pot                poker.Chips
	Actor              int
	Seats              []seatPanel
	Board              []*playingCard
	Actions            []button
	History, Payouts   []string
	Choices            []scenarioChoice
	Notice             string
	Settled, CanDeal   bool
}

func buildPage(view poker.View, mode scenario, version uint64, history []string, notice string) page {
	p := page{
		Version: version, Hand: view.Hand, Street: streetLabel(view.Street),
		Mode: mode.name, Hint: mode.description, Small: view.Config.SmallBlind,
		Big: view.Config.BigBlind, BuyIn: view.Config.BuyIn, Current: view.Current,
		Ceiling: view.Ceiling, Actor: view.Actor + 1, Board: make([]*playingCard, 5),
		History: history, Notice: notice, Settled: view.Result != nil,
	}
	slices.Reverse(p.History)
	for _, choice := range scenarios {
		p.Choices = append(p.Choices, scenarioChoice{ID: choice.id, Name: choice.name, Selected: choice.id == mode.id})
	}
	for i, card := range view.Board {
		face := cardFace(card)
		p.Board[i] = &face
	}
	var funded int
	for i, seat := range view.Seats {
		panel := seatPanel{
			Number: i + 1, Stack: seat.Stack, Wager: seat.Street,
			Live: seat.InHand, Folded: seat.Folded, Acting: i == view.Actor,
			Rebuy: p.Settled && seat.Occupied && seat.Stack == 0,
		}
		switch {
		case i == view.Button && i == view.Small:
			panel.Badges = append(panel.Badges, "D", "SB")
		case i == view.Button:
			panel.Badges = append(panel.Badges, "D")
		case i == view.Small:
			panel.Badges = append(panel.Badges, "SB")
		case i == view.Big:
			panel.Badges = append(panel.Badges, "BB")
		}
		if seat.AllIn {
			panel.Badges = append(panel.Badges, "All-in")
		}
		for _, card := range seat.Cards {
			panel.Cards = append(panel.Cards, cardFace(card))
		}
		if seat.Stack > 0 {
			funded++
		}
		p.Pot += seat.Total
		p.Seats = append(p.Seats, panel)
	}
	for _, option := range view.Actions {
		action := button{Label: actionLabel(option), Kind: option.Action.Kind, Target: option.Action.Target}
		if option.AllIn && option.Action.Kind != poker.AllIn {
			action.Detail = "All-in"
		} else if option.Action.Kind == poker.Raise {
			action.Detail = fmt.Sprintf("Add %d chips", option.Add)
		}
		p.Actions = append(p.Actions, action)
	}
	if result := view.Result; result != nil {
		p.CanDeal = funded >= 2
		p.Pot = 0
		p.Current, p.Ceiling = 0, 0
		for i, pot := range result.Pots {
			p.Pot += pot.Amount
			name := "Main pot"
			if i > 0 {
				name = fmt.Sprintf("Side pot %d", i)
			}
			var winners []string
			for _, seat := range pot.Winners {
				winners = append(winners, fmt.Sprintf("Seat %d", seat+1))
			}
			p.Payouts = append(p.Payouts, fmt.Sprintf("%s · %d chips → %s", name, pot.Amount, strings.Join(winners, ", ")))
		}
		for seat, refund := range result.Refunds {
			if refund > 0 {
				p.Payouts = append(p.Payouts, fmt.Sprintf("Seat %d gets %d uncalled chips back", seat+1, refund))
			}
		}
	}
	return p
}

func actionLabel(option poker.Option) string {
	switch option.Action.Kind {
	case poker.Check:
		return "Check"
	case poker.Fold:
		return "Fold"
	case poker.Call:
		return fmt.Sprintf("Call %d", option.Add)
	case poker.Bet:
		return fmt.Sprintf("Bet %d", option.To)
	case poker.Raise:
		return fmt.Sprintf("Raise to %d", option.To)
	default:
		return fmt.Sprintf("All-in to %d", option.To)
	}
}

func streetLabel(street poker.Street) string {
	switch street {
	case poker.Preflop:
		return "Preflop"
	case poker.Flop:
		return "Flop"
	case poker.Turn:
		return "Turn"
	case poker.River:
		return "River"
	case poker.Settled:
		return "Hand complete"
	default:
		return "Waiting"
	}
}

func cardFace(card poker.Card) playingCard {
	ranks := [...]string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"}
	suits := [...]string{"♣", "♦", "♥", "♠"}
	names := [...]string{"clubs", "diamonds", "hearts", "spades"}
	rank, suit := ranks[card.Rank()-2], card.Suit()
	return playingCard{
		Rank: rank, Suit: suits[suit], Label: rank + " of " + names[suit],
		Red: suit == poker.Hearts || suit == poker.Diamonds,
	}
}
