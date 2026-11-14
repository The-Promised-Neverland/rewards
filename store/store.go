// Package store implements the rewards data layer on a single DynamoDB table.
//
// Item layout (table "Rewards", keys PK and SK, both strings):
//
//	Balance      PK=USER#<userID>   SK=BALANCE                  stars, coins, gems, updatedAt
//	Ledger       PK=USER#<userID>   SK=LEDGER#<nanos>#<refID>   kind, collectible, qty, paymentId, createdAt
//	Idempotency  PK=REQ#<paymentID> SK=EARN                     userId, collectible, expiresAt (TTL)
package store

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// Collectible is also the name of the balance attribute that stores its count.
type Collectible string

const (
	Star Collectible = "stars"
	Coin Collectible = "coins"
	Gem  Collectible = "gems"
)

var allCollectibles = []Collectible{Star, Coin, Gem}

// How long we remember a payment ID. It must outlast any realistic retry window.
const idempotencyTTL = 7 * 24 * time.Hour

var (
	ErrInvalidInput    = errors.New("invalid input")
	ErrPaymentConflict = errors.New("paymentID was already used by a different user")
	ErrRetryable       = errors.New("temporary conflict or throttling, safe to retry")
)

// DynamoAPI is the subset of the DynamoDB client we use, so tests can fake it.
type DynamoAPI interface {
	TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

type Store struct {
	db    DynamoAPI
	table string
	now   func() time.Time
	pick  func() Collectible
}

func New(db DynamoAPI, table string) *Store {
	return &Store{db: db, table: table, now: time.Now, pick: equalOdds}
}

// equalOdds gives every collectible the same chance. Swap for configurable odds later.
func equalOdds() Collectible {
	return allCollectibles[rand.IntN(len(allCollectibles))]
}