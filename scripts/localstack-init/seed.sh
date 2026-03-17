#!/bin/bash
set -euo pipefail

ENDPOINT="${AWS_ENDPOINT_URL:-http://localhost:4566}"
export AWS_PAGER=""
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"

aws() {
    command aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"
}

# Track results for summary table
RESULTS=()

record() {
    RESULTS+=("$1|$2|$3")
}

ensure_s3_bucket() {
    if aws s3api head-bucket --bucket "$1" &>/dev/null; then
        record "S3 Bucket" "$1" "exists"
    else
        aws s3 mb "s3://$1" >/dev/null
        record "S3 Bucket" "$1" "created"
    fi
}

ensure_iam_role() {
    local name="$1"
    local policy="$2"
    if aws iam get-role --role-name "$name" &>/dev/null; then
        record "IAM Role" "$name" "exists"
    else
        aws iam create-role --role-name "$name" --assume-role-policy-document "$policy" >/dev/null
        record "IAM Role" "$name" "created"
    fi
}

ensure_dynamodb_table() {
    if aws dynamodb describe-table --table-name "$1" &>/dev/null; then
        record "DynamoDB Table" "$1" "exists"
    else
        aws dynamodb create-table \
            --table-name "$1" \
            --key-schema AttributeName=id,KeyType=HASH \
            --attribute-definitions AttributeName=id,AttributeType=S \
            --billing-mode PAY_PER_REQUEST >/dev/null
        record "DynamoDB Table" "$1" "created"
    fi
}

ensure_s3_object() {
    local bucket="$1"
    local key="$2"
    local content="$3"
    echo "$content" | aws s3 cp - "s3://$bucket/$key" >/dev/null
    record "S3 Object" "s3://$bucket/$key" "uploaded"
}

print_summary() {
    local type_w=16 name_w=34 status_w=10
    local total_w=$((type_w + name_w + status_w + 4))
    local line
    line=$(printf '─%.0s' $(seq 1 "$total_w"))

    printf "\n%s\n" "$line"
    printf "%-${type_w}s  %-${name_w}s  %-${status_w}s\n" "TYPE" "NAME" "STATUS"
    printf "%s\n" "$line"
    for entry in "${RESULTS[@]}"; do
        IFS='|' read -r type name status <<< "$entry"
        printf "%-${type_w}s  %-${name_w}s  %-${status_w}s\n" "$type" "$name" "$status"
    done
    printf "%s\n" "$line"
    printf "%d resources ready.\n\n" "${#RESULTS[@]}"
}

echo "Seeding LocalStack at $ENDPOINT..."

# S3 buckets
ensure_s3_bucket test-bucket-1
ensure_s3_bucket test-bucket-2
ensure_s3_bucket logs-bucket
ensure_s3_object test-bucket-1 test-file.txt "hello world"

# IAM roles
EC2_TRUST='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}'
LAMBDA_TRUST='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}'

ensure_iam_role test-role "$EC2_TRUST"
ensure_iam_role lambda-execution-role "$LAMBDA_TRUST"

# DynamoDB tables
ensure_dynamodb_table test-table

print_summary
