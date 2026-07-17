package config_test

import (
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/platform/config"
)

func TestNewSchemaRejectsDuplicateAndUnknownClass(t *testing.T) {
	if _, err := config.NewSchema(
		config.FieldSpec{Name: "a", ReloadClass: config.Hot},
		config.FieldSpec{Name: "a", ReloadClass: config.Restart},
	); err == nil {
		t.Fatal("expected an error for a duplicate field name")
	}
	if _, err := config.NewSchema(config.FieldSpec{Name: "a", ReloadClass: "not-a-class"}); err == nil {
		t.Fatal("expected an error for an unknown reload class")
	}
	if _, err := config.NewSchema(config.FieldSpec{Name: "", ReloadClass: config.Hot}); err == nil {
		t.Fatal("expected an error for an empty field name")
	}
}

func TestRequiredReloadClassPicksMostRestrictive(t *testing.T) {
	schema := testSchema(t)

	class, allRegistered := schema.RequiredReloadClass([]string{"runtime.log_level"})
	if class != config.Hot || !allRegistered {
		t.Fatalf("got (%q, %v), want (Hot, true)", class, allRegistered)
	}

	class, allRegistered = schema.RequiredReloadClass([]string{"runtime.log_level", "composition.modules"})
	if class != config.Generation || !allRegistered {
		t.Fatalf("got (%q, %v), want (Generation, true)", class, allRegistered)
	}

	class, allRegistered = schema.RequiredReloadClass([]string{"runtime.log_level", "storage.postgres.dsn"})
	if class != config.Recovery || !allRegistered {
		t.Fatalf("got (%q, %v), want (Recovery, true)", class, allRegistered)
	}

	class, allRegistered = schema.RequiredReloadClass(nil)
	if class != config.Hot || !allRegistered {
		t.Fatalf("got (%q, %v), want (Hot, true) for no changed fields", class, allRegistered)
	}
}

func TestRequiredReloadClassTreatsUnregisteredFieldAsRecovery(t *testing.T) {
	schema := testSchema(t)
	class, allRegistered := schema.RequiredReloadClass([]string{"runtime.log_level", "totally.unknown"})
	if allRegistered {
		t.Fatal("expected allRegistered = false")
	}
	if class != config.Recovery {
		t.Fatalf("class = %q, want %q", class, config.Recovery)
	}
}

func TestChangedFieldsDetectsAddedRemovedAndModified(t *testing.T) {
	previous := []byte(`{"a":"1","b":"2"}`)
	candidate := []byte(`{"a":"1","b":"3","c":"4"}`)

	changed, err := config.ChangedFields(previous, candidate)
	if err != nil {
		t.Fatalf("ChangedFields: %v", err)
	}
	want := map[string]bool{"b": true, "c": true}
	if len(changed) != len(want) {
		t.Fatalf("changed = %v, want keys of %v", changed, want)
	}
	for _, f := range changed {
		if !want[f] {
			t.Fatalf("unexpected changed field %q", f)
		}
	}
}

func TestChangedFieldsAgainstEmptyPreviousTreatsEveryFieldAsChanged(t *testing.T) {
	changed, err := config.ChangedFields(nil, []byte(`{"a":"1","b":"2"}`))
	if err != nil {
		t.Fatalf("ChangedFields: %v", err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %v, want 2 fields", changed)
	}
}

func TestPlatformSchemaDeclaresAllFourReloadClasses(t *testing.T) {
	schema := config.PlatformSchema()
	seen := map[config.ReloadClass]bool{}
	for _, field := range []string{"runtime.log_level", "runtime.environment", "composition.modules", "storage.postgres.dsn"} {
		class, ok := schema.ReloadClassOf(field)
		if !ok {
			t.Fatalf("PlatformSchema does not register field %q", field)
		}
		seen[class] = true
	}
	for _, want := range []config.ReloadClass{config.Hot, config.Restart, config.Generation, config.Recovery} {
		if !seen[want] {
			t.Fatalf("PlatformSchema never uses reload class %q", want)
		}
	}
}
