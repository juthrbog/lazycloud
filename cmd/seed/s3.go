package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	awslib "github.com/juthrbog/lazycloud/internal/aws"
)

type s3Seeder struct {
	client *awslib.Client
	config S3SeedConfig
	region string
}

func newS3Seeder(client *awslib.Client, cfg S3SeedConfig, region string) *s3Seeder {
	return &s3Seeder{client: client, config: cfg, region: region}
}

func (s *s3Seeder) Name() string { return "s3" }

func (s *s3Seeder) Seed(ctx context.Context) error {
	s3c := s.client.S3Client()

	// List existing buckets (single API call for idempotency).
	existing, err := s.listExistingBuckets(ctx, s3c)
	if err != nil {
		return fmt.Errorf("listing buckets: %w", err)
	}

	// Seed base buckets.
	fmt.Printf("  S3 buckets...")
	for _, b := range s.config.Buckets {
		if err := s.ensureBucket(ctx, b.Name, existing); err != nil {
			return err
		}
	}

	// Seed extra generated buckets.
	for i := range s.config.ExtraBuckets {
		name := fmt.Sprintf("data-bucket-%03d", i+1)
		if err := s.ensureBucket(ctx, name, existing); err != nil {
			return err
		}
	}
	fmt.Println(" done")

	// Upload objects for base buckets.
	fmt.Printf("  S3 objects...")
	for bucket, objects := range baseBucketObjects() {
		for _, obj := range objects {
			if err := s.putObject(ctx, s3c, bucket, obj.key, obj.content); err != nil {
				return fmt.Errorf("putting %s/%s: %w", bucket, obj.key, err)
			}
		}
	}

	// Upload generated objects for extra buckets.
	for i := range s.config.ExtraBuckets {
		bucket := fmt.Sprintf("data-bucket-%03d", i+1)
		for j := range s.config.ObjectsPerExtraBucket {
			key := fmt.Sprintf("file-%03d.txt", j+1)
			content := fmt.Sprintf("Generated test file %03d for %s.", j+1, bucket)
			if err := s.putObject(ctx, s3c, bucket, key, content); err != nil {
				return fmt.Errorf("putting %s/%s: %w", bucket, key, err)
			}
		}
	}
	fmt.Println(" done")

	return nil
}

func (s *s3Seeder) listExistingBuckets(ctx context.Context, s3c *s3.Client) (map[string]bool, error) {
	out, err := s3c.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(out.Buckets))
	for _, b := range out.Buckets {
		m[aws.ToString(b.Name)] = true
	}
	return m, nil
}

func (s *s3Seeder) ensureBucket(ctx context.Context, name string, existing map[string]bool) error {
	if existing[name] {
		return nil
	}
	s3c := s.client.S3ClientForRegion(s.region)
	input := &s3.CreateBucketInput{
		Bucket: aws.String(name),
	}
	if s.region != "us-east-1" {
		input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(s.region),
		}
	}
	_, err := s3c.CreateBucket(ctx, input)
	return err
}

func (s *s3Seeder) putObject(ctx context.Context, s3c *s3.Client, bucket, key, content string) error {
	_, err := s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(content),
	})
	return err
}

// ─── Hardcoded object content for base buckets ──────────────────────────────

type objectDef struct {
	key     string
	content string
}

func baseBucketObjects() map[string][]objectDef {
	return map[string][]objectDef{
		"test-bucket-1": {
			{"readme.md", "# Test Bucket 1\n\nThis is a test bucket for LazyCloud development.\n\n## Contents\n\n- Config files\n- Scripts\n- Data files"},
			{"test-file.txt", "hello world"},
			{"config/app.json", `{"name": "lazycloud", "version": "0.1.0", "debug": false, "port": 8080}`},
			{"config/settings.yaml", "database:\n  host: localhost\n  port: 5432\n  name: myapp\n\nredis:\n  host: localhost\n  port: 6379"},
			{"config/nginx.conf", "server {\n    listen 80;\n    server_name example.com;\n\n    location / {\n        proxy_pass http://localhost:8080;\n    }\n}"},
			{"scripts/deploy.sh", "#!/bin/bash\nset -euo pipefail\n\necho 'Deploying application...'\ndocker compose up -d\necho 'Done.'"},
			{"scripts/cleanup.sh", "#!/bin/bash\necho 'Cleaning up temp files...'\nrm -rf /tmp/app-cache/*\necho 'Cleanup complete.'"},
			{"data/users.csv", "id,name,email,role\n1,Alice,alice@example.com,admin\n2,Bob,bob@example.com,user\n3,Charlie,charlie@example.com,user\n4,Diana,diana@example.com,editor"},
			{"data/metrics.json", `{"requests": 15234, "errors": 42, "latency_p99": 230, "uptime": 99.97}`},
			{"data/notes.txt", "Meeting notes 2026-03-17\n- Discussed S3 integration\n- Reviewed TUI design\n- Next steps: implement deletion"},
			{"terraform/main.tf", "resource \"aws_s3_bucket\" \"example\" {\n  bucket = \"my-bucket\"\n\n  tags = {\n    Environment = \"dev\"\n  }\n}"},
			{"terraform/variables.tf", "variable \"region\" {\n  default = \"us-east-1\"\n}\n\nvariable \"environment\" {\n  default = \"dev\"\n}"},
		},
		"test-bucket-2": {
			{"index.html", "<!DOCTYPE html>\n<html>\n<head><title>Test</title></head>\n<body><h1>Hello from S3</h1></body>\n</html>"},
			{"styles.css", "body {\n  font-family: sans-serif;\n  margin: 0;\n  padding: 20px;\n  background: #1e1e2e;\n  color: #cdd6f4;\n}"},
			{"app.js", "const greet = (name) => {\n  console.log(`Hello, ${name}!`);\n};\n\ngreet('LazyCloud');"},
			{"photos/photo1.jpg", "BINARY_PLACEHOLDER_NOT_A_REAL_IMAGE"},
			{"photos/photo2.png", "BINARY_PLACEHOLDER_NOT_A_REAL_IMAGE"},
			{"docs/guide.md", "# User Guide\n\n## Getting Started\n\n1. Install LazyCloud\n2. Run `./lazycloud`\n3. Browse your AWS resources\n\n## Tips\n\n- Use `/` to filter\n- Press `L` for event log"},
			{"docs/changelog.md", "# Changelog\n\n## v0.1.0\n\n- Initial S3 support\n- Bucket browsing\n- Object preview\n- Presigned URLs"},
			{"backups/db-2026-03-01.sql", "-- Database backup\nCREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);\nINSERT INTO users (name) VALUES ('Alice'), ('Bob');"},
			{"backups/db-2026-03-15.sql", "-- Database backup\nCREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT, email TEXT);\nINSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com');"},
		},
		"logs-bucket": {
			{"app/2026-03-01.log", "[2026-03-01 08:00:00] INFO  Server started on :8080\n[2026-03-01 08:01:23] INFO  Request: GET /api/health\n[2026-03-01 08:05:45] WARN  Slow query: 2.3s\n[2026-03-01 08:10:00] ERROR Connection timeout to database"},
			{"app/2026-03-15.log", "[2026-03-15 09:00:00] INFO  Server started on :8080\n[2026-03-15 09:00:05] INFO  Connected to database\n[2026-03-15 09:15:30] INFO  Request: POST /api/users\n[2026-03-15 09:20:00] INFO  Request: GET /api/users"},
			{"app/2026-03-17.log", "[2026-03-17 10:00:00] INFO  Server started on :8080\n[2026-03-17 10:00:01] INFO  Health check passed\n[2026-03-17 10:30:00] WARN  High memory usage: 85%\n[2026-03-17 11:00:00] ERROR Out of memory"},
			{"access/access.log", "192.168.1.1 - - [17/Mar/2026:10:00:00] \"GET / HTTP/1.1\" 200 1234\n192.168.1.2 - - [17/Mar/2026:10:00:05] \"POST /api HTTP/1.1\" 201 56\n10.0.0.1 - - [17/Mar/2026:10:01:00] \"GET /health HTTP/1.1\" 200 2"},
			{"errors/errors.json", `{"timestamp": "2026-03-17T10:30:00Z", "level": "error", "message": "Connection refused", "service": "api", "trace_id": "abc123"}`},
		},
	}
}
