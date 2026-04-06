package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/juthrbog/lazycloud/internal/aws"
)

func main() {
	size := flag.String("size", "small", "seed tier: small, medium, large, enterprise")
	service := flag.String("service", "", "comma-separated services to seed (default: all)")
	endpoint := flag.String("endpoint", "http://localhost:4566", "LocalStack endpoint URL")
	region := flag.String("region", "us-east-1", "AWS region")
	wipe := flag.Bool("wipe", false, "wipe all LocalStack state and exit")
	flag.Parse()

	if *wipe {
		if err := wipeState(*endpoint); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := loadConfig(*size)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	client, err := aws.NewClient("", *region, *endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating AWS client: %v\n", err)
		os.Exit(1)
	}

	seeders := buildSeeders(client, cfg, *region)
	if *service != "" {
		seeders = filterSeeders(seeders, *service)
	}

	if len(seeders) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no matching services to seed")
		os.Exit(1)
	}

	fmt.Printf("Seeding LocalStack at %s (%s tier)...\n", *endpoint, *size)

	ctx := context.Background()
	if err := runSeeders(ctx, seeders); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Seeding complete.")
}

func buildSeeders(client *aws.Client, cfg *SeedConfig, region string) []Seeder {
	return []Seeder{
		newS3Seeder(client, cfg.S3, region),
		newEC2Seeder(client, cfg.EC2),
	}
}

func filterSeeders(seeders []Seeder, filter string) []Seeder {
	want := make(map[string]bool)
	for _, s := range strings.Split(filter, ",") {
		want[strings.TrimSpace(strings.ToLower(s))] = true
	}
	var filtered []Seeder
	for _, s := range seeders {
		if want[s.Name()] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func runSeeders(ctx context.Context, seeders []Seeder) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, s := range seeders {
		g.Go(func() error { return s.Seed(ctx) })
	}
	return g.Wait()
}
