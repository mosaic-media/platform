package domain

import "time"

// HealthState is the readiness state of a Platform component.
type HealthState string

const (
	HealthHealthy     HealthState = "healthy"
	HealthDegraded    HealthState = "degraded"
	HealthUnavailable HealthState = "unavailable"
)

// HealthStatus is a point-in-time readiness report for a Platform
// component.
type HealthStatus struct {
	Component string
	State     HealthState
	Detail    string
	CheckedAt time.Time
}
