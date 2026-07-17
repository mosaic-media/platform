package domain

import "time"

// UserStatus is an admin-managed account status (MEG-015 §09 — Users:
// "admin-managed status").
type UserStatus string

const (
	// UserActive is the default status: the account may authenticate.
	UserActive UserStatus = "active"
	// UserSuspended means an admin has suspended the account. Suspension is
	// account-level, distinct from session revocation: a suspended user's
	// existing sessions are not automatically revoked by this slice.
	UserSuspended UserStatus = "suspended"
)

// User is a local Platform user account.
type User struct {
	ID          UserID
	Username    string
	Email       string
	DisplayName string
	Status      UserStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
