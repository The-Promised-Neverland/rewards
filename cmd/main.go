// Demo of earn, share and redeem, including retries with the same IDs.
//
//	AWS_REGION=ap-south-1 TABLE=Rewards go run ./cmd/earn
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"rewards/store"
)

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	table := os.Getenv("TABLE")
	if table == "" {
		table = "Rewards"
	}
	s := store.New(dynamodb.NewFromConfig(cfg), table)

	// Unique per run so re-running the demo earns fresh collectibles.
	run := time.Now().Unix()
	alice, bob := fmt.Sprintf("alice-%d", run), fmt.Sprintf("bob-%d", run)

	// 1. Earn: 40 payments for alice. Retrying one shows idempotency.
	for i := 0; i < 40; i++ {
		if _, err := s.Earn(ctx, alice, fmt.Sprintf("pay-%d-%d", run, i)); err != nil {
			log.Fatalf("earn %d: %v", i, err)
		}
	}
	dup, err := s.Earn(ctx, alice, fmt.Sprintf("pay-%d-0", run))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("retry of earn pay-0: duplicate=%v\n", dup.Duplicate)

	bal, err := s.GetBalance(ctx, alice)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("alice after earning: stars=%d coins=%d gems=%d\n", bal.Stars, bal.Coins, bal.Gems)

	// 2. Share 2 stars from alice to bob. Retry with the same requestID.
	shareID := fmt.Sprintf("share-%d", run)
	for i := 1; i <= 2; i++ {
		res, err := s.Share(ctx, alice, bob, store.Star, 2, shareID)
		if err != nil {
			log.Fatalf("share attempt %d: %v", i, err)
		}
		fmt.Printf("share attempt %d: duplicate=%v\n", i, res.Duplicate)
	}

	// 3. Redeem a set of stars, if alice still has enough. Retry the same requestID.
	bal, _ = s.GetBalance(ctx, alice)
	if bal.Stars >= 5 {
		redeemID := fmt.Sprintf("redeem-%d", run)
		for i := 1; i <= 2; i++ {
			res, err := s.Redeem(ctx, alice, redeemID, store.Star)
			if err != nil {
				log.Fatalf("redeem attempt %d: %v", i, err)
			}
			fmt.Printf("redeem attempt %d: payout=%d paise duplicate=%v\n", i, res.PayoutPaise, res.Duplicate)
		}
	}

	// 4. Sharing more than alice has must fail cleanly.
	_, err = s.Share(ctx, alice, bob, store.Gem, 1000, fmt.Sprintf("share-big-%d", run))
	fmt.Printf("share 1000 gems: %v\n", err)

	// 5. History: alice's ledger, newest first, two pages of 5.
	cursor := ""
	for page := 1; page <= 2; page++ {
		h, err := s.History(ctx, alice, 5, cursor)
		if err != nil {
			log.Fatalf("history page %d: %v", page, err)
		}
		fmt.Printf("history page %d:\n", page)
		for _, e := range h.Entries {
			fmt.Printf("  %-15s %-5s x%d with=%q ref=%s\n", e.Kind, e.Collectible, e.Qty, e.Counterparty, e.Ref)
		}
		if h.NextCursor == "" {
			break
		}
		cursor = h.NextCursor
	}

	for _, u := range []string{alice, bob} {
		b, _ := s.GetBalance(ctx, u)
		fmt.Printf("%s final: stars=%d coins=%d gems=%d\n", u, b.Stars, b.Coins, b.Gems)
	}
}