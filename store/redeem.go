package store

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SetRule says how many of a collectible make a full set and what the set pays.
// Money is stored in paise (integers), never floats. Hard-coded for now;
// the BRD wants these configurable per campaign.
type SetRule struct {
	Size        int64
	PayoutPaise int64
}

var setRules = map[Collectible]SetRule{
	Star: {Size: 5, PayoutPaise: 1000},  // 5 stars  -> Rs 10
	Coin: {Size: 10, PayoutPaise: 5000}, // 10 coins -> Rs 50
	Gem:  {Size: 8, PayoutPaise: 7000},  // 8 gems   -> Rs 70
}

type RedeemResult struct {
	PayoutPaise int64
	// Duplicate is true when this requestID was already redeemed.
	Duplicate bool
}

// Redeem turns one full set of collectible c into a payout request, exactly once per requestID.
//
// One transaction, four writes (about 8 write units):
//  1. idempotency record, only if the requestID is new
//  2. balance minus the set size, only if the user has at least that many
//  3. REDEEM ledger entry
//  4. payout request item with status PENDING (PK=PAYOUT#<requestID>)
//
// Item 4 is an outbox: a DynamoDB Streams trigger (or poller) picks up PENDING
// payouts and calls the payout service. That part is not built yet.
func (s *Store) Redeem(ctx context.Context, userID, requestID string, c Collectible) (RedeemResult, error) {
	rule, known := setRules[c]
	if userID == "" || requestID == "" || !known {
		return RedeemResult{}, ErrInvalidInput
	}

	now := s.now()
	nowUnix := now.Unix()
	ledgerSK := fmt.Sprintf("LEDGER#%019d#%s", now.UnixNano(), requestID)

	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{
				TableName: &s.table,
				Item: map[string]types.AttributeValue{
					"PK":          str("REQ#" + requestID),
					"SK":          str("REDEEM"),
					"userId":      str(userID),
					"collectible": str(string(c)),
					"payoutPaise": num(rule.PayoutPaise),
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
				UpdateExpression:         aws.String("SET #c = #c - :size, updatedAt = :now"),
				ConditionExpression:      aws.String("#c >= :size"),
				ExpressionAttributeNames: map[string]string{"#c": string(c)},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":size": num(rule.Size),
					":now":  num(nowUnix),
				},
			}},
			{Put: &types.Put{
				TableName: &s.table,
				Item: map[string]types.AttributeValue{
					"PK":          str("USER#" + userID),
					"SK":          str(ledgerSK),
					"kind":        str("REDEEM"),
					"collectible": str(string(c)),
					"qty":         num(rule.Size),
					"payoutPaise": num(rule.PayoutPaise),
					"requestId":   str(requestID),
					"createdAt":   num(nowUnix),
				},
			}},
			{Put: &types.Put{
				TableName: &s.table,
				Item: map[string]types.AttributeValue{
					"PK":          str("PAYOUT#" + requestID),
					"SK":          str("REQUEST"),
					"userId":      str(userID),
					"amountPaise": num(rule.PayoutPaise),
					"status":      str("PENDING"),
					"createdAt":   num(nowUnix),
				},
			}},
		},
	})
	if err == nil {
		return RedeemResult{PayoutPaise: rule.PayoutPaise}, nil
	}

	codes, ok := cancelCodes(err)
	if !ok {
		return RedeemResult{}, fmt.Errorf("redeem: %w", err)
	}
	if len(codes) > 0 && codes[0] == condFailed {
		return s.existingRedeem(ctx, userID, requestID, c)
	}
	if len(codes) > 1 && codes[1] == condFailed {
		return RedeemResult{}, ErrInsufficient
	}
	if code, retry := firstRetryable(codes); retry {
		return RedeemResult{}, fmt.Errorf("%w: %s", ErrRetryable, code)
	}
	return RedeemResult{}, fmt.Errorf("redeem: %w", err)
}

func (s *Store) existingRedeem(ctx context.Context, userID, requestID string, c Collectible) (RedeemResult, error) {
	item, err := s.getRequest(ctx, requestID, "REDEEM")
	if err != nil {
		return RedeemResult{}, err
	}
	if getS(item, "userId") != userID || getS(item, "collectible") != string(c) {
		return RedeemResult{}, ErrRequestConflict
	}
	return RedeemResult{PayoutPaise: getN(item, "payoutPaise"), Duplicate: true}, nil
}
