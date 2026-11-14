package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Balance struct {
	Stars, Coins, Gems int64
}

// Get returns the count for one collectible type.
func (b Balance) Get(c Collectible) int64 {
	switch c {
	case Star:
		return b.Stars
	case Coin:
		return b.Coins
	case Gem:
		return b.Gems
	}
	return 0
}

// GetBalance returns the user's inventory. A user with no item yet has zero of everything.
func (s *Store) GetBalance(ctx context.Context, userID string) (Balance, error) {
	if userID == "" {
		return Balance{}, ErrInvalidInput
	}
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": str("USER#" + userID),
			"SK": str("BALANCE"),
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return Balance{}, fmt.Errorf("get balance: %w", err)
	}
	if out.Item == nil {
		return Balance{}, nil
	}
	return Balance{
		Stars: getN(out.Item, string(Star)),
		Coins: getN(out.Item, string(Coin)),
		Gems:  getN(out.Item, string(Gem)),
	}, nil
}

func getN(item map[string]types.AttributeValue, key string) int64 {
	if v, ok := item[key].(*types.AttributeValueMemberN); ok {
		if n, err := strconv.ParseInt(v.Value, 10, 64); err == nil {
			return n
		}
	}
	return 0
}
