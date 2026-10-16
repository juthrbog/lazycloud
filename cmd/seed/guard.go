package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The seed tool creates VPCs, security groups that are open to the
// internet, buckets, and queues. It must never run against a real AWS
// account. aws.NewClient uses dummy credentials when it gets an endpoint,
// but with an empty endpoint it uses the normal credential chain and
// AWS_PROFILE. These checks run before any AWS call.

// httpClient has a timeout, so a wrong endpoint fails fast instead of
// hanging.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// checkEndpoint returns an error unless endpoint is an http or https URL
// with a local host. allowRemote accepts any host, for LocalStack on another
// machine.
func checkEndpoint(endpoint string, allowRemote bool) error {
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("--endpoint is empty. The seed tool runs only against LocalStack, for example --endpoint http://localhost:4566")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("--endpoint %q is not an http or https URL", endpoint)
	}
	if !allowRemote && !isLocalHost(u.Hostname()) {
		return fmt.Errorf("--endpoint host %q is not local. Use localhost, a loopback address, localstack, "+
			"or a localhost.localstack.cloud name, or pass --allow-remote for LocalStack on another machine", u.Hostname())
	}
	return nil
}

// isLocalHost reports whether host names this machine or the LocalStack
// container.
func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	switch {
	case host == "localhost", host == "localstack":
		return true
	case host == "localhost.localstack.cloud", strings.HasSuffix(host, ".localhost.localstack.cloud"):
		return true // LocalStack's own DNS name, which resolves to 127.0.0.1
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkLocalStack asks the endpoint for the LocalStack health report. Real
// AWS and other servers do not answer this path with a list of services.
func checkLocalStack(ctx context.Context, endpoint string) error {
	healthURL := strings.TrimRight(endpoint, "/") + "/_localstack/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("no LocalStack at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("no LocalStack at %s: %s returned %s", endpoint, healthURL, resp.Status)
	}
	var health struct {
		Services map[string]string `json:"services"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil || len(health.Services) == 0 {
		return fmt.Errorf("no LocalStack at %s: %s did not return a LocalStack health report", endpoint, healthURL)
	}
	return nil
}
