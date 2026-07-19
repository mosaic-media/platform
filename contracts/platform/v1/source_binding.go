package v1

import "time"

// MatchMethod records how a source was matched to a Node (ADR 0013).
// Closed and Platform-owned: identity resolution is Platform machinery.
type MatchMethod string

const (
	// MatchExternalIDExact matched on a provider identifier — the strongest
	// signal available.
	MatchExternalIDExact MatchMethod = "external_id_exact"
	// MatchFingerprint matched on a content fingerprint.
	MatchFingerprint MatchMethod = "fingerprint"
	// MatchFuzzyTitle matched on title similarity, the method most likely
	// to need review.
	MatchFuzzyTitle MatchMethod = "fuzzy_title"
	// MatchUserSelected was chosen by a person.
	MatchUserSelected MatchMethod = "user_selected"
)

// BindingStatus is where a binding sits in identity resolution.
type BindingStatus string

const (
	// BindingConfirmed is a settled binding. A merge is exactly this: a
	// confirmed, high-confidence binding.
	BindingConfirmed BindingStatus = "confirmed"
	// BindingPendingReview is a weak match. ADR 0013 makes identity
	// resolution explicit rather than implicit: a weak match lands here and
	// surfaces to the user rather than silently merging two different works
	// that happen to share a title.
	BindingPendingReview BindingStatus = "pending_review"
	// BindingRejected is a match a user declined, kept so the same weak
	// match is not proposed again.
	BindingRejected BindingStatus = "rejected"
)

// SourceBinding ties an external source to a Node with an explicit,
// inspectable confidence (ADR 0013).
//
// The operations follow from the shape rather than needing their own
// concepts. A merge is a confirmed high-confidence binding. A split moves a
// binding to a different Node — the source is never re-fingerprinted and
// nothing else in the graph needs to know. When a Node's last binding is
// removed it becomes NodeOrphaned, not deleted. Those transitions are Platform
// operations issued through the resolve command, not methods on this value.
type SourceBinding struct {
	ID     SourceBindingID
	NodeID NodeID
	// SourceProvider names where the source came from — a metadata
	// provider, a filesystem scanner, a debrid service.
	SourceProvider string
	// SourceRef identifies the source within that provider. Provider and
	// ref are unique together: one source binds to at most one Node, which
	// is what makes a split a move rather than a copy.
	SourceRef string
	// MatchConfidence is between 0 and 1 inclusive, enforced by the
	// database.
	MatchConfidence float64
	MatchMethod     MatchMethod
	Status          BindingStatus
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// NeedsReview reports whether the binding is waiting on a person.
func (b SourceBinding) NeedsReview() bool { return b.Status == BindingPendingReview }
