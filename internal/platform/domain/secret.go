package domain

import "time"

// SecretRef names a secret without revealing its value or storage location.
type SecretRef struct {
	Name string
}

// Secret is a resolved secret value.
type Secret struct {
	Ref       SecretRef
	Value     string
	Version   int
	RotatedAt time.Time
}
