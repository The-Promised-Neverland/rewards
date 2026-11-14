package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

var ErrInvalidCursor = errors.New("invalid cursor")

// Entry is one line of a user's ledger.
type Entry struct {
	Kind         string // EARN, SHARE_SENT, SHARE_RECEIVED or REDEEM
	Collectible  Collectible
	Qty          int64
	Counterparty string // the other user, for shares
	Ref          string // paymentID or requestID that caused the entry
	PayoutPaise  int64  // only for REDEEM
	CreatedAt    time.Time
}

type HistoryPage struct {
	Entries []Entry
	// NextCursor is empty on the last page. Pass it back to get the next page.
	NextCursor string
}

// History returns a user's ledger, newest first, one page at a time.
// limit <= 0 uses the default page size; the maximum is 100.
func (s *Store) History(ctx context.Context, userID string, limit int, cursor string) (HistoryPage, error) {
	if userID == "" {
		return HistoryPage{}, ErrInvalidInput
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	in := &dynamodb.QueryInput{
		TableName:              &s.table,
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     str("USER#" + userID),
			":prefix": str("LEDGER#"),
		},
		ScanIndexForward: aws.Bool(false), // newest first
		Limit:            aws.Int32(int32(limit)),
	}
	if cursor != "" {
		start, err := decodeCursor(userID, cursor)
		if err != nil {
			return HistoryPage{}, err
		}
		in.ExclusiveStartKey = start
	}

	out, err := s.db.Query(ctx, in)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("history: %w", err)
	}

	page := HistoryPage{Entries: make([]Entry, 0, len(out.Items))}
	for _, item := range out.Items {
		ref := getS(item, "paymentId")
		if ref == "" {
			ref = getS(item, "requestId")
		}
		page.Entries = append(page.Entries, Entry{
			Kind:         getS(item, "kind"),
			Collectible:  Collectible(getS(item, "collectible")),
			Qty:          getN(item, "qty"),
			Counterparty: getS(item, "counterparty"),
			Ref:          ref,
			PayoutPaise:  getN(item, "payoutPaise"),
			CreatedAt:    time.Unix(getN(item, "createdAt"), 0),
		})
	}
	if len(out.LastEvaluatedKey) > 0 {
		page.NextCursor = encodeCursor(out.LastEvaluatedKey)
	}
	return page, nil
}

func encodeCursor(key map[string]types.AttributeValue) string {
	raw, _ := json.Marshal(struct{ PK, SK string }{getS(key, "PK"), getS(key, "SK")})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor turns a client-supplied cursor back into a DynamoDB start key.
// The cursor comes from outside, so it must belong to this user and point into
// the ledger. Otherwise a client could page through someone else's data.
func decodeCursor(userID, cursor string) (map[string]types.AttributeValue, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var k struct{ PK, SK string }
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, ErrInvalidCursor
	}
	if k.PK != "USER#"+userID || !strings.HasPrefix(k.SK, "LEDGER#") {
		return nil, ErrInvalidCursor
	}
	return map[string]types.AttributeValue{"PK": str(k.PK), "SK": str(k.SK)}, nil
}