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

ensure_s3_bucket() {
    aws s3api head-bucket --bucket "$1" &>/dev/null || aws s3 mb "s3://$1" >/dev/null
}

ensure_s3_object() {
    echo -e "$3" | aws s3 cp - "s3://$1/$2" >/dev/null
}

# Returns the AMI ID for an image with the given name, or empty string if not found.
find_ami() {
    aws ec2 describe-images --owners self \
        --filters "Name=name,Values=$1" \
        --query "Images[0].ImageId" --output text 2>/dev/null | grep -v None || true
}

ensure_ami() {
    local name="$1" arch="$2"
    local existing
    existing=$(find_ami "$name")
    if [ -n "$existing" ]; then
        echo "$existing"
        return
    fi
    aws ec2 register-image \
        --name "$name" \
        --architecture "$arch" \
        --root-device-name /dev/xvda \
        --virtualization-type hvm \
        --query "ImageId" --output text
}

# Returns instance ID(s) tagged with the given Name, or empty string.
find_instance() {
    aws ec2 describe-instances \
        --filters "Name=tag:Name,Values=$1" "Name=instance-state-name,Values=pending,running,stopped,stopping" \
        --query "Reservations[0].Instances[0].InstanceId" --output text 2>/dev/null | grep -v None || true
}

ensure_instance() {
    local name="$1" ami_id="$2" type="$3"
    local existing
    existing=$(find_instance "$name")
    if [ -n "$existing" ]; then
        echo "$existing"
        return
    fi
    aws ec2 run-instances \
        --image-id "$ami_id" \
        --instance-type "$type" \
        --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=$name}]" \
        --query "Instances[0].InstanceId" --output text
}

echo "Seeding LocalStack at $ENDPOINT..."

# ─── S3 ─────────────────────────────────────────────────────────────────────

printf "  S3 buckets..."
ensure_s3_bucket test-bucket-1
ensure_s3_bucket test-bucket-2
ensure_s3_bucket logs-bucket
echo " done"

printf "  S3 objects..."
ensure_s3_object test-bucket-1 readme.md "# Test Bucket 1\n\nThis is a test bucket for LazyCloud development.\n\n## Contents\n\n- Config files\n- Scripts\n- Data files"
ensure_s3_object test-bucket-1 test-file.txt "hello world"
ensure_s3_object test-bucket-1 config/app.json '{"name": "lazycloud", "version": "0.1.0", "debug": false, "port": 8080}'
ensure_s3_object test-bucket-1 config/settings.yaml "database:\n  host: localhost\n  port: 5432\n  name: myapp\n\nredis:\n  host: localhost\n  port: 6379"
ensure_s3_object test-bucket-1 config/nginx.conf "server {\n    listen 80;\n    server_name example.com;\n\n    location / {\n        proxy_pass http://localhost:8080;\n    }\n}"
ensure_s3_object test-bucket-1 scripts/deploy.sh "#!/bin/bash\nset -euo pipefail\n\necho 'Deploying application...'\ndocker compose up -d\necho 'Done.'"
ensure_s3_object test-bucket-1 scripts/cleanup.sh "#!/bin/bash\necho 'Cleaning up temp files...'\nrm -rf /tmp/app-cache/*\necho 'Cleanup complete.'"
ensure_s3_object test-bucket-1 data/users.csv "id,name,email,role\n1,Alice,alice@example.com,admin\n2,Bob,bob@example.com,user\n3,Charlie,charlie@example.com,user\n4,Diana,diana@example.com,editor"
ensure_s3_object test-bucket-1 data/metrics.json '{"requests": 15234, "errors": 42, "latency_p99": 230, "uptime": 99.97}'
ensure_s3_object test-bucket-1 data/notes.txt "Meeting notes 2026-03-17\n- Discussed S3 integration\n- Reviewed TUI design\n- Next steps: implement deletion"
ensure_s3_object test-bucket-1 terraform/main.tf 'resource "aws_s3_bucket" "example" {\n  bucket = "my-bucket"\n\n  tags = {\n    Environment = "dev"\n  }\n}'
ensure_s3_object test-bucket-1 terraform/variables.tf 'variable "region" {\n  default = "us-east-1"\n}\n\nvariable "environment" {\n  default = "dev"\n}'

# test-bucket-2: images and binary-like files (for testing preview guard)
ensure_s3_object test-bucket-2 index.html "<!DOCTYPE html>\n<html>\n<head><title>Test</title></head>\n<body><h1>Hello from S3</h1></body>\n</html>"
ensure_s3_object test-bucket-2 styles.css "body {\n  font-family: sans-serif;\n  margin: 0;\n  padding: 20px;\n  background: #1e1e2e;\n  color: #cdd6f4;\n}"
ensure_s3_object test-bucket-2 app.js "const greet = (name) => {\n  console.log(\`Hello, \${name}!\`);\n};\n\ngreet('LazyCloud');"
ensure_s3_object test-bucket-2 photos/photo1.jpg "BINARY_PLACEHOLDER_NOT_A_REAL_IMAGE"
ensure_s3_object test-bucket-2 photos/photo2.png "BINARY_PLACEHOLDER_NOT_A_REAL_IMAGE"
ensure_s3_object test-bucket-2 docs/guide.md "# User Guide\n\n## Getting Started\n\n1. Install LazyCloud\n2. Run \`./lazycloud\`\n3. Browse your AWS resources\n\n## Tips\n\n- Use \`/\` to filter\n- Press \`L\` for event log"
ensure_s3_object test-bucket-2 docs/changelog.md "# Changelog\n\n## v0.1.0\n\n- Initial S3 support\n- Bucket browsing\n- Object preview\n- Presigned URLs"
ensure_s3_object test-bucket-2 backups/db-2026-03-01.sql "-- Database backup\nCREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);\nINSERT INTO users (name) VALUES ('Alice'), ('Bob');"
ensure_s3_object test-bucket-2 backups/db-2026-03-15.sql "-- Database backup\nCREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT, email TEXT);\nINSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com');"

# logs-bucket: log files
ensure_s3_object logs-bucket app/2026-03-01.log "[2026-03-01 08:00:00] INFO  Server started on :8080\n[2026-03-01 08:01:23] INFO  Request: GET /api/health\n[2026-03-01 08:05:45] WARN  Slow query: 2.3s\n[2026-03-01 08:10:00] ERROR Connection timeout to database"
ensure_s3_object logs-bucket app/2026-03-15.log "[2026-03-15 09:00:00] INFO  Server started on :8080\n[2026-03-15 09:00:05] INFO  Connected to database\n[2026-03-15 09:15:30] INFO  Request: POST /api/users\n[2026-03-15 09:20:00] INFO  Request: GET /api/users"
ensure_s3_object logs-bucket app/2026-03-17.log "[2026-03-17 10:00:00] INFO  Server started on :8080\n[2026-03-17 10:00:01] INFO  Health check passed\n[2026-03-17 10:30:00] WARN  High memory usage: 85%\n[2026-03-17 11:00:00] ERROR Out of memory"
ensure_s3_object logs-bucket access/access.log "192.168.1.1 - - [17/Mar/2026:10:00:00] \"GET / HTTP/1.1\" 200 1234\n192.168.1.2 - - [17/Mar/2026:10:00:05] \"POST /api HTTP/1.1\" 201 56\n10.0.0.1 - - [17/Mar/2026:10:01:00] \"GET /health HTTP/1.1\" 200 2"
ensure_s3_object logs-bucket errors/errors.json '{"timestamp": "2026-03-17T10:30:00Z", "level": "error", "message": "Connection refused", "service": "api", "trace_id": "abc123"}'
echo " done"

# ─── EC2 AMIs ────────────────────────────────────────────────────────────────

printf "  EC2 AMIs..."
AMI_AL2=$(ensure_ami "lazycloud-amazonlinux2-x86_64" "x86_64")
AMI_UBUNTU=$(ensure_ami "lazycloud-ubuntu2204-x86_64" "x86_64")
AMI_ARM=$(ensure_ami "lazycloud-ubuntu2204-arm64" "arm64")
AMI_MINIMAL=$(ensure_ami "lazycloud-minimal-x86_64" "x86_64")
echo " done"

# ─── EC2 Instances ───────────────────────────────────────────────────────────

printf "  EC2 instances..."
INST_WEB=$(ensure_instance "web-server-1"    "$AMI_AL2"    "t3.micro")
INST_API=$(ensure_instance "api-server-1"    "$AMI_UBUNTU" "t3.small")
INST_DB=$(ensure_instance  "db-primary"      "$AMI_UBUNTU" "t3.medium")
INST_ARM=$(ensure_instance "worker-arm-1"    "$AMI_ARM"    "t4g.micro")
INST_OLD=$(ensure_instance "old-bastion"     "$AMI_MINIMAL" "t2.micro")

# Stop a couple so we have a mix of running/stopped in the list
aws ec2 stop-instances --instance-ids "$INST_DB" "$INST_OLD" >/dev/null 2>&1 || true
echo " done"

echo "Seeding complete."
