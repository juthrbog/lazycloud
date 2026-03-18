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

echo "Wiping LocalStack at $ENDPOINT..."

# S3: delete all objects then all buckets
printf "  S3 buckets..."
for bucket in $(aws s3api list-buckets --query 'Buckets[].Name' --output text 2>/dev/null); do
    aws s3 rm "s3://$bucket" --recursive >/dev/null 2>&1 || true
    aws s3api delete-bucket --bucket "$bucket" >/dev/null 2>&1 || true
done
echo " done"

# DynamoDB: delete all tables
printf "  DynamoDB tables..."
for table in $(aws dynamodb list-tables --query 'TableNames[]' --output text 2>/dev/null); do
    aws dynamodb delete-table --table-name "$table" >/dev/null 2>&1 || true
done
echo " done"

# IAM: delete all non-default roles
printf "  IAM roles..."
for role in $(aws iam list-roles --query 'Roles[].RoleName' --output text 2>/dev/null); do
    # Detach managed policies
    for policy_arn in $(aws iam list-attached-role-policies --role-name "$role" --query 'AttachedPolicies[].PolicyArn' --output text 2>/dev/null); do
        aws iam detach-role-policy --role-name "$role" --policy-arn "$policy_arn" >/dev/null 2>&1 || true
    done
    # Delete inline policies
    for policy in $(aws iam list-role-policies --role-name "$role" --query 'PolicyNames[]' --output text 2>/dev/null); do
        aws iam delete-role-policy --role-name "$role" --policy-name "$policy" >/dev/null 2>&1 || true
    done
    aws iam delete-role --role-name "$role" >/dev/null 2>&1 || true
done
echo " done"

# EC2: terminate all instances
printf "  EC2 instances..."
instance_ids=$(aws ec2 describe-instances --query 'Reservations[].Instances[?State.Name!=`terminated`].InstanceId' --output text 2>/dev/null)
if [ -n "$instance_ids" ]; then
    aws ec2 terminate-instances --instance-ids $instance_ids >/dev/null 2>&1 || true
fi
echo " done"

# EC2: delete security groups (non-default)
printf "  EC2 security groups..."
for sg in $(aws ec2 describe-security-groups --query 'SecurityGroups[?GroupName!=`default`].GroupId' --output text 2>/dev/null); do
    aws ec2 delete-security-group --group-id "$sg" >/dev/null 2>&1 || true
done
echo " done"

# EC2: delete key pairs
printf "  EC2 key pairs..."
for kp in $(aws ec2 describe-key-pairs --query 'KeyPairs[].KeyName' --output text 2>/dev/null); do
    aws ec2 delete-key-pair --key-name "$kp" >/dev/null 2>&1 || true
done
echo " done"

# ECS: delete all services and clusters
printf "  ECS clusters..."
for cluster in $(aws ecs list-clusters --query 'clusterArns[]' --output text 2>/dev/null); do
    for service in $(aws ecs list-services --cluster "$cluster" --query 'serviceArns[]' --output text 2>/dev/null); do
        aws ecs update-service --cluster "$cluster" --service "$service" --desired-count 0 >/dev/null 2>&1 || true
        aws ecs delete-service --cluster "$cluster" --service "$service" --force >/dev/null 2>&1 || true
    done
    aws ecs delete-cluster --cluster "$cluster" >/dev/null 2>&1 || true
done
echo " done"

# Route53: delete all hosted zones (and their records)
printf "  Route53 hosted zones..."
for zone_id in $(aws route53 list-hosted-zones --query 'HostedZones[].Id' --output text 2>/dev/null); do
    # Delete non-default record sets first
    aws route53 list-resource-record-sets --hosted-zone-id "$zone_id" \
        --query 'ResourceRecordSets[?Type!=`NS` && Type!=`SOA`]' --output json 2>/dev/null | \
        jq -c '.[]' 2>/dev/null | while read -r record; do
            change_batch=$(jq -n --argjson record "$record" '{"Changes":[{"Action":"DELETE","ResourceRecordSet":$record}]}')
            aws route53 change-resource-record-sets --hosted-zone-id "$zone_id" --change-batch "$change_batch" >/dev/null 2>&1 || true
        done
    aws route53 delete-hosted-zone --id "$zone_id" >/dev/null 2>&1 || true
done
echo " done"

# CloudWatch Logs: delete all log groups
printf "  CloudWatch log groups..."
for lg in $(aws logs describe-log-groups --query 'logGroups[].logGroupName' --output text 2>/dev/null); do
    aws logs delete-log-group --log-group-name "$lg" >/dev/null 2>&1 || true
done
echo " done"

# Lambda: delete all functions
printf "  Lambda functions..."
for func in $(aws lambda list-functions --query 'Functions[].FunctionName' --output text 2>/dev/null); do
    aws lambda delete-function --function-name "$func" >/dev/null 2>&1 || true
done
echo " done"

# CloudFormation: delete all stacks
printf "  CloudFormation stacks..."
for stack in $(aws cloudformation list-stacks --stack-status-filter CREATE_COMPLETE UPDATE_COMPLETE --query 'StackSummaries[].StackName' --output text 2>/dev/null); do
    aws cloudformation delete-stack --stack-name "$stack" >/dev/null 2>&1 || true
done
echo " done"

echo "LocalStack wipe complete."
