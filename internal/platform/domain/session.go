package domain

import "time"

// Session is an issued Platform session.
type Session struct {
	ID        SessionID
	UserID    UserID
	IssuedAt  time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// Revoked reports whether the session has been explicitly revoked.
func (s Session) Revoked() bool {
	return s.RevokedAt != nil
}

// ExpiredAt reports whether the session is expired as of at.
func (s Session) ExpiredAt(at time.Time) bool {
	return !at.Before(s.ExpiresAt)
}
