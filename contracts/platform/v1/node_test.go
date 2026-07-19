package v1_test

import (
	"testing"

	v1 "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
)

// TestNormaliseTypeName covers the collapse ADR 0015 relies on: the open
// vocabularies are unconstrained text, so spelling variants of one concept
// must land on one value or a library silently splits in two.
func TestNormaliseTypeName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "anime_series", "anime_series"},
		{"title case with a space", "Anime Series", "anime_series"},
		{"hyphenated", "anime-series", "anime_series"},
		{"shouting", "ANIME_SERIES", "anime_series"},
		{"surrounding whitespace", "  anime series  ", "anime_series"},
		{"repeated separators collapse", "anime___series", "anime_series"},
		{"mixed separators collapse", "anime - series", "anime_series"},
		{"leading and trailing separators drop", "__anime_series__", "anime_series"},
		{"unrecognised runes separate rather than vanish", "sci-fi/anime", "sci_fi_anime"},
		{"digits survive", "top_100", "top_100"},
		{"namespace colon survives", "AnimeKit:OVA", "animekit:ova"},
		{"empty stays empty", "", ""},
		{"separators only", " - _ ", ""},

		// The limit of what normalisation can do. These are different values
		// and stay different values; only the media_types registry catches
		// them, which is why it is still owed.
		{"a missing separator is not recoverable", "animeseries", "animeseries"},
		{"a misspelling is not recoverable", "anmie_series", "anmie_series"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := v1.NormaliseTypeName(tc.in); got != tc.want {
				t.Fatalf("NormaliseTypeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormaliseTypeNameIsIdempotent guards the property the stores depend on:
// normalising an already-stored value must not change it, or a read-modify-
// write cycle would drift.
func TestNormaliseTypeNameIsIdempotent(t *testing.T) {
	for _, in := range []string{"Anime Series", "sci-fi/anime", "__x__", "animekit:ova", ""} {
		once := v1.NormaliseTypeName(in)
		if twice := v1.NormaliseTypeName(once); twice != once {
			t.Fatalf("normalising %q twice gave %q then %q", in, once, twice)
		}
	}
}

// TestNodeCanonicalNormalisesEveryOpenVocabulary checks all three open
// columns are covered, not just the one the artist gap exposed.
func TestNodeCanonicalNormalisesEveryOpenVocabulary(t *testing.T) {
	node := v1.Node{
		MediaType:     "Anime Series",
		ContainerType: "Box-Set",
		ItemType:      "Special Feature",
		Title:         "Kept As Written",
	}

	got := node.Canonical()

	if got.MediaType != "anime_series" {
		t.Errorf("MediaType = %q", got.MediaType)
	}
	if got.ContainerType != "box_set" {
		t.Errorf("ContainerType = %q", got.ContainerType)
	}
	if got.ItemType != "special_feature" {
		t.Errorf("ItemType = %q", got.ItemType)
	}
	// The title is content, not vocabulary, and must survive untouched.
	if got.Title != "Kept As Written" {
		t.Errorf("Title = %q, want it left alone", got.Title)
	}
}
