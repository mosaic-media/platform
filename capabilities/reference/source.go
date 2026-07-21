// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package reference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// MetadataSource is the capability's own port to an external provider. The
// Platform offers no HTTP contract and needs none: a capability compiles into
// the binary with trust established before the build (ADR 0007), so it owns
// its provider integration outright. HTTPSource below is one implementation;
// a test supplies another.
type MetadataSource interface {
	// Fetch resolves a free-text query to one work's metadata.
	Fetch(ctx context.Context, query string) (WorkMetadata, error)
}

// WorkMetadata is what the capability sources and then maps onto the
// Platform's generic model. It is the capability's own vocabulary, not the
// Platform's — the capability is responsible for translating a provider's
// shape into works, containers, items and edges.
type WorkMetadata struct {
	Provider  string
	SourceID  string
	Title     string
	MediaType v1.MediaType
	// ExternalIDs is scheme→id, stored on the created work so a later lookup
	// can find it without re-sourcing.
	ExternalIDs map[string]string
	Seasons     []SeasonMetadata
	// Adaptation, when set, is a separate source work this one adapts — an
	// anime and its source manga are two Works joined by an edge (ADR 0013),
	// which this capability must honour rather than fold into one tree.
	Adaptation *AdaptationMetadata
}

// SeasonMetadata is a container layer with its items.
type SeasonMetadata struct {
	Title    string
	Episodes []EpisodeMetadata
}

// EpisodeMetadata is an item, optionally with a playable file.
type EpisodeMetadata struct {
	Title string
	// FilePath, when set, is a local path to attach as an edition Part.
	FilePath string
	Duration time.Duration
}

// AdaptationMetadata is the source work an anime adapts.
type AdaptationMetadata struct {
	Provider  string
	SourceID  string
	Title     string
	MediaType v1.MediaType
}

// HTTPSource resolves metadata from a JSON provider over HTTP. It is the
// capability's own code — net/http and encoding/json, no Platform
// involvement. The response shape is the provider's; the capability decodes
// and maps it.
type HTTPSource struct {
	BaseURL string
	Client  *http.Client
}

// NewHTTPSource builds an HTTPSource with a bounded default client.
func NewHTTPSource(baseURL string) *HTTPSource {
	return &HTTPSource{BaseURL: baseURL, Client: &http.Client{Timeout: 10 * time.Second}}
}

// wireWork mirrors the JSON a provider returns. Kept unexported: the provider
// shape is an implementation detail behind MetadataSource.
type wireWork struct {
	Provider    string            `json:"provider"`
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	MediaType   string            `json:"media_type"`
	ExternalIDs map[string]string `json:"external_ids"`
	Seasons     []struct {
		Title    string `json:"title"`
		Episodes []struct {
			Title       string `json:"title"`
			FilePath    string `json:"file_path"`
			DurationSec int    `json:"duration_sec"`
		} `json:"episodes"`
	} `json:"seasons"`
	Adaptation *struct {
		Provider  string `json:"provider"`
		ID        string `json:"id"`
		Title     string `json:"title"`
		MediaType string `json:"media_type"`
	} `json:"adaptation"`
}

// Fetch queries the provider and maps its response into WorkMetadata.
func (s *HTTPSource) Fetch(ctx context.Context, query string) (WorkMetadata, error) {
	endpoint := s.BaseURL + "/work?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return WorkMetadata{}, fmt.Errorf("build request: %w", err)
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		return WorkMetadata{}, fmt.Errorf("fetch metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return WorkMetadata{}, fmt.Errorf("provider returned status %d", resp.StatusCode)
	}

	var w wireWork
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return WorkMetadata{}, fmt.Errorf("decode metadata: %w", err)
	}
	return mapWireWork(w), nil
}

// encodeExternalIDs renders a scheme→id map as the flat JSON document
// Node.ExternalIDs expects (ADR 0013). An empty map becomes an empty object.
func encodeExternalIDs(ids map[string]string) []byte {
	if len(ids) == 0 {
		return []byte(`{}`)
	}
	// json.Marshal of a map[string]string cannot fail, so the error is
	// discarded rather than complicating every call site.
	doc, _ := json.Marshal(ids)
	return doc
}

func mapWireWork(w wireWork) WorkMetadata {
	meta := WorkMetadata{
		Provider:    w.Provider,
		SourceID:    w.ID,
		Title:       w.Title,
		MediaType:   v1.MediaType(w.MediaType),
		ExternalIDs: w.ExternalIDs,
	}
	for _, ws := range w.Seasons {
		season := SeasonMetadata{Title: ws.Title}
		for _, we := range ws.Episodes {
			season.Episodes = append(season.Episodes, EpisodeMetadata{
				Title:    we.Title,
				FilePath: we.FilePath,
				Duration: time.Duration(we.DurationSec) * time.Second,
			})
		}
		meta.Seasons = append(meta.Seasons, season)
	}
	if w.Adaptation != nil {
		meta.Adaptation = &AdaptationMetadata{
			Provider:  w.Adaptation.Provider,
			SourceID:  w.Adaptation.ID,
			Title:     w.Adaptation.Title,
			MediaType: v1.MediaType(w.Adaptation.MediaType),
		}
	}
	return meta
}
