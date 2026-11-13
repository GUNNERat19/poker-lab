package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"cappedpoker/internal/privatedeal"
)

func main() {
	seats := flag.Int("seats", 3, "number of simulated seats")
	mode := flag.String("withhold", "", "simulate a missing shuffle or board share (shuffle or reveal)")
	flag.Parse()
	if *mode != "" && *mode != "shuffle" && *mode != "reveal" {
		log.Fatal("withhold must be shuffle or reveal")
	}
	start := time.Now()
	report, err := privatedeal.Simulate(*seats, func(step privatedeal.Step) bool {
		return step.Seat == *seats-1 &&
			((*mode == "shuffle" && step.Stage == "shuffle") || (*mode == "reveal" && step.Stage == "board share"))
	})
	if errors.Is(err, privatedeal.ErrAborted) {
		fmt.Printf("%v after %s. No completed deal.\n", err, time.Since(start).Round(time.Millisecond))
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Verified %d shuffles (%d proof bytes) and dealt %d private hands, %d board cards, and %d showdown hands in %s.\n",
		report.ShuffleCount, report.ProofBytes, len(report.HoleCards), len(report.Board), len(report.Showdown),
		report.Elapsed.Round(time.Millisecond))
}
