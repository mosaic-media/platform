package config

import (
	"context"
	"fmt"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/secrets"
)

// Manager runs the MEG-015 §08 configuration activation state machine
// (Draft -> Validated -> Active -> Superseded, with Validated -> Rejected
// on failed validation) against whichever ConfigStore a caller supplies —
// mirroring sessions.Manager, so application services can use it against
// both a transaction-scoped store (the write path) and a direct one.
type Manager struct {
	clock  contracts.Clock
	ids    contracts.IDGenerator
	schema *Schema
}

// NewManager builds a Manager backed by clock, ids and schema.
func NewManager(clock contracts.Clock, ids contracts.IDGenerator, schema *Schema) *Manager {
	return &Manager{clock: clock, ids: ids, schema: schema}
}

// Draft saves payload as a new, unvalidated configuration candidate.
func (m *Manager) Draft(ctx context.Context, store contracts.ConfigStore, payload []byte) (domain.ConfigVersion, error) {
	if len(payload) == 0 {
		return domain.ConfigVersion{}, contracts.NewError(contracts.InvalidArgument, "config payload is required")
	}
	if _, err := decodeFields(payload); err != nil {
		return domain.ConfigVersion{}, contracts.WrapError(contracts.InvalidArgument, "config payload must be a JSON object", err)
	}

	version := domain.ConfigVersion{
		ID:        domain.ConfigVersionID(m.ids.NewID()),
		Payload:   payload,
		Status:    domain.ConfigDraft,
		CreatedAt: m.clock.Now(),
	}
	return store.Save(ctx, version)
}

// Validate checks a Draft version's payload against the schema — every
// field registered, and every Secret field holding a well-formed secret://
// reference rather than a raw value (MEG-015 §08 — Secret References) —
// and moves it to Validated or Rejected. Both outcomes are a successful
// call, not a Platform error — rejection is a normal, informative result
// of the validate transition, not a failure to validate.
func (m *Manager) Validate(ctx context.Context, store contracts.ConfigStore, id domain.ConfigVersionID) (domain.ConfigVersion, error) {
	version, err := store.FindByID(ctx, id)
	if err != nil {
		return domain.ConfigVersion{}, err
	}
	if !version.CanValidate() {
		return domain.ConfigVersion{}, contracts.NewError(contracts.Conflict,
			fmt.Sprintf("config version %s is %s, not draft", id, version.Status))
	}

	now := m.clock.Now()
	fields, err := FieldNames(version.Payload)
	if err != nil {
		return store.UpdateStatus(ctx, version.MarkRejected(now, err.Error()))
	}
	for _, field := range fields {
		if _, ok := m.schema.ReloadClassOf(field); !ok {
			detail := fmt.Sprintf("field %q is not a registered configuration field", field)
			return store.UpdateStatus(ctx, version.MarkRejected(now, detail))
		}
		if m.schema.IsSecret(field) {
			value, err := FieldValue(version.Payload, field)
			if err != nil || !secrets.IsRef(value) {
				detail := fmt.Sprintf("field %q must hold a secret:// reference, not a raw value", field)
				return store.UpdateStatus(ctx, version.MarkRejected(now, detail))
			}
		}
	}

	return store.UpdateStatus(ctx, version.MarkValidated(now, "schema and policy checks passed"))
}

// ActivationOutcome is the result of Activate. Activated is true only when
// the change was Hot-classified and applied immediately; otherwise the
// version remains Validated and ReloadClass reports what is required
// before it can take effect.
type ActivationOutcome struct {
	Version     domain.ConfigVersion
	Activated   bool
	ReloadClass ReloadClass
}

// Activate attempts to activate a Validated version. It diffs the
// candidate's payload against the currently Active version's payload (none
// if this is the first activation) and classifies the change by the most
// restrictive reload class among the changed fields. A Hot-only change
// activates immediately, superseding the previous Active version in the
// same call. Any more restrictive change is correctly classified and
// flagged rather than fake-applied: the version stays Validated, and
// escalating it (a restart, a new Generation via Supervisor, or the
// recovery flow) is a later slice's responsibility.
func (m *Manager) Activate(ctx context.Context, store contracts.ConfigStore, id domain.ConfigVersionID) (ActivationOutcome, error) {
	version, err := store.FindByID(ctx, id)
	if err != nil {
		return ActivationOutcome{}, err
	}
	if !version.CanActivate() {
		return ActivationOutcome{}, contracts.NewError(contracts.Conflict,
			fmt.Sprintf("config version %s is %s, not validated", id, version.Status))
	}

	current, err := store.FindActive(ctx)
	switch {
	case err == nil:
		// current holds the version to diff against and, if this change is
		// Hot, to supersede below.
	case contracts.CategoryOf(err) == contracts.NotFound:
		current = domain.ConfigVersion{} // fresh install: nothing to supersede
	default:
		return ActivationOutcome{}, err
	}

	changed, err := ChangedFields(current.Payload, version.Payload)
	if err != nil {
		return ActivationOutcome{}, contracts.WrapError(contracts.Internal, "diff config payload", err)
	}

	class, _ := m.schema.RequiredReloadClass(changed)
	if class != Hot {
		return ActivationOutcome{Version: version, Activated: false, ReloadClass: class}, nil
	}

	now := m.clock.Now()

	// Supersede the previous Active version before activating the new one:
	// at most one version may ever be Active (enforced structurally by a
	// unique index in the PostgreSQL adapter), so the two must never be
	// Active at the same time, even momentarily.
	if current.ID != "" && current.ID != version.ID {
		if _, err := store.UpdateStatus(ctx, current.MarkSuperseded(now)); err != nil {
			return ActivationOutcome{}, err
		}
	}

	activated, err := store.UpdateStatus(ctx, version.MarkActive(now))
	if err != nil {
		return ActivationOutcome{}, err
	}

	return ActivationOutcome{Version: activated, Activated: true, ReloadClass: Hot}, nil
}
