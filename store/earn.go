package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type EarnResult struct {
	Collectible Collectible
	// Duplicate is true when this paymentID was already processed. The result
	// is the original one, so a client retry gets the same answer.
	Duplicate bool
}

// Cancellation codes that mean "try again", not "this request is wrong".
var retryableCodes = map[string]bool{
	"TransactionConflict":           true,
	"ThrottlingError":               true,
	"ProvisionedThroughputExceeded": true,
}

// Earn grants one random collectible for a qualifying payment, exactly once per paymentID.
//
// One transaction, three writes (about 6 write units, since transactions cost double):
//  1. idempotency record, only if the paymentID has never been seen
//  2. balance increment (ADD creates the item and attribute on first use)
//  3. ledger entry
func (s *Store) Earn(ctx context.Context, userID, paymentID string) (EarnResult, error) {
	if userID == "" || paymentID == "" {
		return EarnResult{}, ErrInvalidInput
	}

	picked := s.pick()
	now := s.now()
	nowUnix := now.Unix()
	// Nanosecond prefix keeps ledger entries time-ordered; paymentID suffix makes
	// the key unique even if two events share a timestamp.
	ledgerSK := fmt.Sprintf("LEDGER#%019d#%s", now.UnixNano(), paymentID)

	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{
				TableName: &s.table,
				Item: map[string]types.AttributeValue{
					"PK":          str("REQ#" + paymentID),
					"SK":          str("EARN"),
					"userId":      str(userID),
					"collectible": str(string(picked)),
					"expiresAt":   num(now.Add(idempotencyTTL).Unix()),
				},
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			}},
			{Update: &types.Update{
				TableName: &s.table,
				Key: map[string]types.AttributeValue{
					"PK": str("USER#" + userID),
					"SK": str("BALANCE"),
				},
				UpdateExpression:         aws.String("ADD #c :one SET updatedAt = :now"),
				ExpressionAttributeNames: map[string]string{"#c": string(picked)},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":one": num(1),
					":now": num(nowUnix),
				},
			}},
			{Put: &types.Put{
				TableName: &s.table,
				Item: map[string]types.AttributeValue{
					"PK":          str("USER#" + userID),
					"SK":          str(ledgerSK),
					"kind":        str("EARN"),
					"collectible": str(string(picked)),
					"qty":         num(1),
					"paymentId":   str(paymentID),
					"createdAt":   num(nowUnix),
				},
			}},
		},
	})
	if err == nil {
		return EarnResult{Collectible: picked}, nil
	}
	return s.handleEarnError(ctx, err, userID, paymentID)
}

func (s *Store) handleEarnError(ctx context.Context, err error, userID, paymentID string) (EarnResult, error) {
	var tce *types.TransactionCanceledException
	if !errors.As(err, &tce) {
		return EarnResult{}, fmt.Errorf("earn: %w", err)
	}
	for i, reason := range tce.CancellationReasons {
		code := aws.ToString(reason.Code)
		// Item 0 is the idempotency record: failing its condition means "already done".
		if i == 0 && code == "ConditionalCheckFailed" {
			return s.existingEarn(ctx, userID, paymentID)
		}
		if retryableCodes[code] {
			return EarnResult{}, fmt.Errorf("%w: %s", ErrRetryable, code)
		}
	}
	return EarnResult{}, fmt.Errorf("earn: %w", err)
}

// existingEarn returns the original result for an already processed paymentID.
func (s *Store) existingEarn(ctx context.Context, userID, paymentID string) (EarnResult, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": str("REQ#" + paymentID),
			"SK": str("EARN"),
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return EarnResult{}, fmt.Errorf("earn: reading idempotency record: %w", err)
	}
	if out.Item == nil {
		return EarnResult{}, fmt.Errorf("earn: idempotency record for %s missing after conflict", paymentID)
	}
	if getS(out.Item, "userId") != userID {
		return EarnResult{}, ErrPaymentConflict
	}
	return EarnResult{Collectible: Collectible(getS(out.Item, "collectible")), Duplicate: true}, nil
}

func str(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func num(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}

func getS(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}
