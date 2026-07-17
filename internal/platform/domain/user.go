package domain

import "time"

// User is a local Platform user account.
type User struct {
	ID          UserID
	Username    string
	Email       string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
