// Package contracts holds private Platform contract definitions used before
// SDK extraction (MEG-015 §03). Contracts pass domain value types across
// their boundary and report failures through the Platform ErrorCategory
// scheme; they never leak database rows or driver-specific types.
package contracts
