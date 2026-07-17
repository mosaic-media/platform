// Package app hosts application services, transaction orchestration and
// command handling (MEG-015 §04). Every command follows the same order:
// validate shape, authenticate the caller, authorize through policy, open a
// UnitOfWork, load state through contracts, apply domain rules, persist
// state and outbox events in the same transaction, then return a Platform
// result type. Queries follow the same authenticate/authorize gate before
// reading through a contract.
package app
