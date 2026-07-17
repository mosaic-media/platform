package secrets

import (
	"strings"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// scheme is the secret reference URI prefix MEG-015 §08 shows:
// "storage.postgres.password = secret://platform/postgres/password".
const scheme = "secret://"

// IsRef reports whether value is shaped like a secret reference, so config
// validation and other consumers can distinguish a reference from a literal
// value without fully parsing it.
func IsRef(value string) bool {
	return strings.HasPrefix(value, scheme)
}

// ParseRef parses a secret:// reference URI into a domain.SecretRef.
func ParseRef(uri string) (domain.SecretRef, error) {
	if !IsRef(uri) {
		return domain.SecretRef{}, contracts.NewError(contracts.InvalidArgument, "not a secret reference: missing secret:// scheme")
	}
	name := strings.TrimPrefix(uri, scheme)
	if name == "" {
		return domain.SecretRef{}, contracts.NewError(contracts.InvalidArgument, "secret reference has no name")
	}
	return domain.SecretRef{Name: name}, nil
}

// FormatRef renders ref back into its secret:// URI form, e.g. for storing
// in a configuration value.
func FormatRef(ref domain.SecretRef) string {
	return scheme + ref.Name
}
