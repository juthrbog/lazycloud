package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// wipeState resets all LocalStack state via the internal reset endpoint.
func wipeState(ctx context.Context, endpoint string) error {
	fmt.Printf("Wiping LocalStack state...")

	resetURL := strings.TrimRight(endpoint, "/") + "/_localstack/state/reset"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resetURL, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to LocalStack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reset returned %d: %s", resp.StatusCode, body)
	}

	fmt.Println(" done")
	return nil
}
