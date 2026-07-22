// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package config

import "fmt"

// FieldSpec declares one configuration field's reload class.
type FieldSpec struct {
	Name        string
	ReloadClass ReloadClass
	// Secret marks a field whose value must be a secret:// reference,
	// never a raw value. Validate rejects a Secret field whose value is
	// not a well-formed reference, so a secret value can never reach a
	// persisted configuration version.
	Secret bool
}

// Schema is the structural registry of every known configuration field and
// its declared reload class. Callers query it to learn whether a change can
// hot-apply, instead of relying on documentation.
type Schema struct {
	fields map[string]FieldSpec
}

// NewSchema builds a Schema from fields. It rejects an empty field name, a
// duplicate field name, or an undeclared reload class, since an
// unclassified field must never be allowed to reach the activation flow.
func NewSchema(fields ...FieldSpec) (*Schema, error) {
	s := &Schema{fields: make(map[string]FieldSpec, len(fields))}
	for _, f := range fields {
		if f.Name == "" {
			return nil, fmt.Errorf("config: field name is required")
		}
		if _, exists := s.fields[f.Name]; exists {
			return nil, fmt.Errorf("config: duplicate field %q", f.Name)
		}
		if !f.ReloadClass.valid() {
			return nil, fmt.Errorf("config: field %q declares unknown reload class %q", f.Name, f.ReloadClass)
		}
		s.fields[f.Name] = f
	}
	return s, nil
}

// ReloadClassOf returns the declared reload class for field, and whether it
// is registered at all.
func (s *Schema) ReloadClassOf(field string) (ReloadClass, bool) {
	f, ok := s.fields[field]
	return f.ReloadClass, ok
}

// IsSecret reports whether field is declared as a secret-reference field.
// An unregistered field reports false; Validate's registration check rejects
// it before this ever matters.
func (s *Schema) IsSecret(field string) bool {
	return s.fields[field].Secret
}

// RequiredReloadClass returns the most restrictive reload class declared
// among changedFields, and reports whether every one of them is a
// registered field. An unregistered field is treated as Recovery — the
// most restrictive class — since a field the schema does not know about
// must never be assumed safe to hot-apply.
func (s *Schema) RequiredReloadClass(changedFields []string) (class ReloadClass, allRegistered bool) {
	class = Hot
	allRegistered = true
	for _, field := range changedFields {
		fieldClass, ok := s.fields[field]
		if !ok {
			allRegistered = false
			class = moreRestrictive(class, Recovery)
			continue
		}
		class = moreRestrictive(class, fieldClass.ReloadClass)
	}
	return class, allRegistered
}

// PlatformSchema is the first-cut registry of Platform configuration
// fields and their reload classes. It illustrates all four classes against
// concepts already defined elsewhere in the architecture:
//
//   - runtime.log_level: the canonical hot-reload example.
//   - runtime.environment: matches the existing bootstrap Config.Environment
//     placeholder; switching environment requires a process restart.
//   - composition.modules: which Modules are compiled into the running
//     binary. A Generation is exactly "Platform, Shell and admitted Modules",
//     so changing module composition requires the Supervisor to activate a
//     new Generation.
//   - storage.postgres.dsn: the primary datastore is explicitly outside the
//     Generation boundary — the PostgreSQL database is never inside a
//     Generation — so changing it is a recovery-flow action, not a hot toggle
//     or a Generation swap.
//   - storage.postgres.password: a Secret field — its value must be a
//     secret:// reference such as
//     "storage.postgres.password = secret://platform/postgres/password".
//     Grouped with the DSN under the same Recovery class, since both name
//     the same storage-connection concern.
func PlatformSchema() *Schema {
	schema, err := NewSchema(
		FieldSpec{Name: "runtime.log_level", ReloadClass: Hot},
		FieldSpec{Name: "runtime.environment", ReloadClass: Restart},
		FieldSpec{Name: "composition.modules", ReloadClass: Generation},
		FieldSpec{Name: "storage.postgres.dsn", ReloadClass: Recovery},
		FieldSpec{Name: "storage.postgres.password", ReloadClass: Recovery, Secret: true},
		// Telemetry retention, per signal (ADR 0058). Hot because shortening
		// retention should take effect without a restart — an administrator
		// reclaiming disk should not have to bounce the process to do it.
		//
		// Audit is listed here for completeness of the vocabulary, but it is
		// the one an operator may not freely shorten: ADR 0057 floors it in
		// code, not in config, because "set audit retention to an hour, act,
		// wait" is the standard way to defeat an audit log. The audit store
		// itself is not built yet.
		FieldSpec{Name: "telemetry.retention.logs_days", ReloadClass: Hot},
		FieldSpec{Name: "telemetry.retention.traces_hours", ReloadClass: Hot},
		FieldSpec{Name: "telemetry.retention.metrics_days", ReloadClass: Hot},
		FieldSpec{Name: "telemetry.retention.audit_days", ReloadClass: Hot},
	)
	if err != nil {
		// Unreachable: the field list above is a fixed, valid literal.
		panic(err)
	}
	return schema
}
