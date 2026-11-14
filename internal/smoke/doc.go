// Package smoke holds end-to-end tests that run the whole app against
// LocalStack. The tests have the build tag smoke, so `go test ./...` does
// not run them. Run them with `task smoke`, which starts LocalStack and
// seeds it first.
package smoke
