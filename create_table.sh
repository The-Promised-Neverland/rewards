#!/usr/bin/env bash
# Creates the single Rewards table (on-demand billing) and enables TTL on expiresAt.
# Usage: AWS_REGION=ap-south-1 ./scripts/create_table.sh
set -euo pipefail

TABLE="${TABLE:-Rewards}"

aws dynamodb create-table \
  --table-name "$TABLE" \
  --attribute-definitions AttributeName=PK,AttributeType=S AttributeName=SK,AttributeType=S \
  --key-schema AttributeName=PK,KeyType=HASH AttributeName=SK,KeyType=RANGE \
  --billing-mode PAY_PER_REQUEST

aws dynamodb wait table-exists --table-name "$TABLE"

aws dynamodb update-time-to-live \
  --table-name "$TABLE" \
  --time-to-live-specification Enabled=true,AttributeName=expiresAt

echo "Table $TABLE ready."
