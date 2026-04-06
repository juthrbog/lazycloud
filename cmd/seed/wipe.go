package main

import (
	"fmt"
	"io"
	"net/http"
)

// wipeState resets all LocalStack state via the internal reset endpoint.
func wipeState(endpoint string) error {
	fmt.Printf("Wiping LocalStack state...")

	resp, err := http.Post(endpoint+"/_localstack/state/reset", "", nil)
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
