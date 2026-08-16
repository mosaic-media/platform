// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// UserStatus is an admin-managed account status.
type UserStatus string

const (
	// UserActive is the default status: the account may authenticate.
	UserActive UserStatus = "active"
	// UserSuspended means an administrator has suspended the account: it is
	// refused a new session, and every session it already held is revoked when
	// the suspension is applied. Both halves are required — refusing only new
	// sessions would leave a signed-in device working for the ninety days a
	// refresh chain lives.
	//
	// It is distinct from revocation: revoking ends one device, this ends the
	// account. Reactivating restores the account and deliberately not the
	// sessions — a device that was signed out signs in again.
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
