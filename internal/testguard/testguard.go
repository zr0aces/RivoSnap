// Package testguard stops destructive integration tests from running against
// a database or Redis that is not one of the disposable loopback containers
// started by scripts/test-integration.sh. It is imported only by tests.
package testguard

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
)

// Require skips the test unless GALLERY_INTEGRATION is set. When it is set,
// it fails the test before any connection unless DATABASE_URL (and REDIS_URL,
// when set) point at a loopback host.
func Require(t testing.TB) {
	t.Helper()
	if os.Getenv("GALLERY_INTEGRATION") == "" {
		t.Skip("skipping integration test; set GALLERY_INTEGRATION=1")
	}
	if err := check(os.Getenv("DATABASE_URL"), os.Getenv("REDIS_URL")); err != nil {
		t.Fatal(err)
	}
}

func check(databaseURL, redisURL string) error {
	if databaseURL == "" {
		return errors.New("testguard: DATABASE_URL is required for integration tests")
	}
	if err := checkLoopback("DATABASE_URL", databaseURL); err != nil {
		return err
	}
	if redisURL != "" {
		return checkLoopback("REDIS_URL", redisURL)
	}
	return nil
}

func checkLoopback(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		// Never include the raw value: it can contain a password.
		return fmt.Errorf("testguard: %s is not a valid URL; refusing to run integration tests", name)
	}
	if u.Query().Has("host") {
		return fmt.Errorf("testguard: %s sets a host query parameter; refusing to run integration tests", name)
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return nil
	default:
		return fmt.Errorf("testguard: %s host %q is not loopback; integration tests only run against the disposable containers from scripts/test-integration.sh", name, u.Hostname())
	}
}
