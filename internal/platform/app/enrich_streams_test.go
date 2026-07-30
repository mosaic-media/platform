// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"reflect"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Stream enrichment had no test, which is most of why it silently narrowed
// every candidate it attached (ADR 0073, and M3 slice 7).

// TestAResolvedCandidateKeepsWhatItsSourceSaid is the case that was wrong. A
// module parses the container and both codecs at its own boundary (ADR 0051)
// and this pass wrote neither, so a Part materialised through the fan-out knew
// less than the module that resolved it and the probe was the only thing that
// ever learned what a release was.
func TestAResolvedCandidateKeepsWhatItsSourceSaid(t *testing.T) {
	got := partCommandFor("node-1", 3, v1.StreamLink{
		Label: "Torrentio", Title: "Some.Release.2160p.HDR.mkv",
		SizeBytes:  61_400_000_000,
		Location:   v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: "aiostreams", Ref: "https://cdn/x"},
		Container:  "mkv",
		VideoCodec: "hevc",
		AudioCodec: "eac3",
	})

	if got.Container != "mkv" || got.VideoCodec != "hevc" || got.AudioCodec != "eac3" {
		t.Errorf("container/video/audio = %q/%q/%q, want mkv/hevc/eac3 — a candidate must not arrive saying less than its source said",
			got.Container, got.VideoCodec, got.AudioCodec)
	}
	// The fields that already crossed, so the change did not trade one loss for
	// another.
	if got.EditionLabel != "Some.Release.2160p.HDR.mkv" {
		t.Errorf("EditionLabel = %q, want the release title", got.EditionLabel)
	}
	if got.SizeBytes != 61_400_000_000 {
		t.Errorf("SizeBytes = %d, want it carried", got.SizeBytes)
	}
	if got.Location.Ref != "https://cdn/x" || got.Location.Scheme != v1.RemoteLocation {
		t.Errorf("Location = %+v, want it carried", got.Location)
	}
	if got.NaturalOrder != 3 {
		t.Errorf("NaturalOrder = %v, want the provider's own ranking preserved", got.NaturalOrder)
	}
	if got.Role != v1.PartEdition {
		t.Errorf("Role = %q, want %q", got.Role, v1.PartEdition)
	}
}

// TestALabelStandsInForAMissingTitle covers the fallback: a source that gave
// only a short name still produces a Part somebody can tell apart from another.
func TestALabelStandsInForAMissingTitle(t *testing.T) {
	got := partCommandFor("node-1", 0, v1.StreamLink{Label: "Torrentio"})
	if got.EditionLabel != "Torrentio" {
		t.Errorf("EditionLabel = %q, want the label when there is no title", got.EditionLabel)
	}
}

// TestEveryFieldTheTwoTypesShareIsCarried is the general form, and it is the
// test that would have caught the original defect rather than describing it.
//
// `StreamLink` and `AttachContentPartCommand` name their descriptive fields
// identically **on purpose** — the SDK says so: "a consumer copying a link onto
// a Part should be moving values across, not translating them." So any field
// the two types share by name *and* type is one this mapping owes, and a new one
// added to both and forgotten here is exactly the mistake that already happened
// once. Reflection asks the types rather than a list somebody has to remember to
// extend.
//
// Fields named differently on the two sides — Title and Label become
// EditionLabel — are outside this check by construction, and covered above.
func TestEveryFieldTheTwoTypesShareIsCarried(t *testing.T) {
	link := reflect.TypeOf(v1.StreamLink{})
	cmd := reflect.TypeOf(v1.AttachContentPartCommand{})

	// A link with every shared field set to something distinguishable from the
	// zero value, so "carried" and "left empty" cannot be confused.
	filled := reflect.New(link).Elem()
	var shared []string
	for i := range link.NumField() {
		f := link.Field(i)
		target, ok := cmd.FieldByName(f.Name)
		if !ok || target.Type != f.Type {
			continue
		}
		shared = append(shared, f.Name)
		fillNonZero(t, filled.Field(i), f.Name)
	}

	if len(shared) == 0 {
		t.Fatal("no fields are shared by name and type — the two types have diverged, and this check is no longer testing anything")
	}

	got := reflect.ValueOf(partCommandFor("node-1", 0, filled.Interface().(v1.StreamLink)))
	for _, name := range shared {
		want := filled.FieldByName(name)
		if have := got.FieldByName(name); !reflect.DeepEqual(have.Interface(), want.Interface()) {
			t.Errorf("%s = %#v, want %#v — StreamLink and AttachContentPartCommand share this field by name and type, so the mapping owes it",
				name, have.Interface(), want.Interface())
		}
	}
	t.Logf("checked %d shared fields: %v", len(shared), shared)
}

// fillNonZero sets v to a value distinguishable from its zero, for whichever
// kinds these two types actually use. An unhandled kind fails loudly rather than
// silently checking that zero equals zero.
func fillNonZero(t *testing.T, v reflect.Value, name string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("set-" + name)
	case reflect.Int, reflect.Int64:
		v.SetInt(7)
	case reflect.Float64:
		v.SetFloat(7)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				fillNonZero(t, v.Field(i), name+"."+v.Type().Field(i).Name)
			}
		}
	default:
		t.Fatalf("%s is a %s, which this check does not know how to fill — extend fillNonZero rather than letting the field go unchecked", name, v.Kind())
	}
}
