package main

import "context"

// Seeder seeds a single AWS service with test data.
type Seeder interface {
	// Name returns the service name for logging (e.g., "S3", "EC2").
	Name() string
	// Seed creates all resources for this service. It must be idempotent.
	Seed(ctx context.Context) error
}
