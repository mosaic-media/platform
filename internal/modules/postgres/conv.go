package postgres

import "github.com/mosaic-media/mosaic-platform/internal/platform/domain"

// permissionsToStrings and stringsToPermissions convert between the domain's
// []Permission and the []string that pgx maps to a PostgreSQL text[] column.
// Both always return a non-nil slice so an empty set persists as '{}' rather
// than NULL.

func permissionsToStrings(perms []domain.Permission) []string {
	out := make([]string, len(perms))
	for i, p := range perms {
		out[i] = string(p)
	}
	return out
}

func stringsToPermissions(values []string) []domain.Permission {
	out := make([]domain.Permission, len(values))
	for i, v := range values {
		out[i] = domain.Permission(v)
	}
	return out
}
