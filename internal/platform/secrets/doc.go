// Package secrets implements the Platform Secret Broker (MEG-015 §08,
// §12 — Secret broker). Broker prefers the OS keychain (keychain.go) and
// falls back to an encrypted local vault (vault.go) protected by a
// separate recovery key when the keychain is unavailable. ref.go parses
// and formats the secret:// reference URIs configuration values store
// instead of raw secret values.
package secrets
