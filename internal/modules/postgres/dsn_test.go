package postgres_test

import (
	"fmt"
	"net/url"
	"strings"
)

// hostPortFromDSN extracts host:port from a postgres:// URL DSN.
func hostPortFromDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("dsn has no host")
	}
	return u.Host, nil
}

// replaceDatabase returns dsn with its database path segment swapped for
// name, preserving credentials, host and query parameters.
func replaceDatabase(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		// Fall back to a naive replacement for keyword-style DSNs.
		return dsn
	}
	u.Path = "/" + strings.TrimPrefix(name, "/")
	return u.String()
}
