// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ProbeAttribute is the key a probe document is stored under in a Part's
// attributes (platform#29).
//
// The Platform owns the top-level namespacing of that document and the playback
// transport owns what lives under this key. Attributes are unvalidated by design
// (platform#9), so the only thing enforced here is that one writer's document
// cannot silently erase another's.
const ProbeAttribute = "probe"

// RecordPartProbeCommand carries what a probe learned about a release's bytes,
// to be stored on the Part it describes.
//
// The scalar fields and Probe are not alternatives. The scalars are the summary
// candidate ranking and a detail screen read (platform#27); Probe is the whole
// track list, which the scalars cannot express and the per-stream decision
// cannot do without.
type RecordPartProbeCommand struct {
	Caller v1.Caller
	PartID v1.PartID

	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	HDRFormat  string
	SizeBytes  int64

	// Probe is the opaque probe document. The Platform stores it without
	// interpreting it, exactly as it does a module's settings (platform#17).
	Probe []byte
}

// PartProbe is the stored probe document on a Part, nil when it has none
// (platform#29). It is the read half of what RecordPartProbe writes, exported so
// the emit-side can state what a release is — codecs, tracks, languages —
// without being about to play it.
//
// Nil for a Part that has not been probed, which is the ordinary state before
// something has been played once. A caller renders less rather than failing.
func PartProbe(part v1.Part) []byte { return probeAttribute(part) }

// RecordPartProbeResult carries the updated Part.
type RecordPartProbeResult struct {
	Part v1.Part
}

// RecordPartProbe writes a probe result onto the Part it describes (platform#29).
//
// A probe describes bytes, and bytes do not change, so re-deriving the same
// answer on every play is the remaining latency between a click and a first
// frame once the resolution cache has removed the aggregator call.
//
// It is a write on a read path. What is recorded is a fact about a file rather
// than anything about the person, and it would be identical whoever triggered
// it — but it writes to the content graph, so it authorises content.bind. The
// playback transport therefore records as the system principal (platform#13):
// the gate is unchanged and a read-only viewer can still warm the cache, and
// the event's actor is the system, since nobody asked for this write.
func (s *Service) RecordPartProbe(ctx context.Context, cmd RecordPartProbeCommand) (RecordPartProbeResult, error) {
	if cmd.Caller.Session == "" {
		return RecordPartProbeResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.PartID == "" {
		return RecordPartProbeResult{}, contracts.NewError(contracts.InvalidArgument, "part id is required")
	}
	if len(cmd.Probe) > 0 && !json.Valid(cmd.Probe) {
		return RecordPartProbeResult{}, contracts.NewError(contracts.InvalidArgument, "probe document is not valid JSON")
	}

	az, err := s.enter(ctx, cmd.Caller, ActionContentBind, policy.Resource{Type: "content"})
	if err != nil {
		return RecordPartProbeResult{}, err
	}

	var result RecordPartProbeResult

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		part, err := tx.Parts().FindByID(ctx, cmd.PartID)
		if err != nil {
			return err
		}

		// The probe is authoritative and overwrites whatever the module parsed
		// from the release name (platform#29, which demoted parsing to a ranking
		// hint). An empty field is still left alone: a probe that could not
		// determine something has not learned that the thing is absent.
		if cmd.Container != "" {
			part.Container = cmd.Container
		}
		if cmd.VideoCodec != "" {
			part.VideoCodec = cmd.VideoCodec
		}
		if cmd.AudioCodec != "" {
			part.AudioCodec = cmd.AudioCodec
		}
		if cmd.Width > 0 {
			part.Width = cmd.Width
		}
		if cmd.Height > 0 {
			part.Height = cmd.Height
		}
		// HDRFormat is assigned unconditionally, because "" is a real answer
		// here: a release the module guessed was HDR from its name, and the
		// bytes say is not, must lose the guess, or the Platform tone-maps an
		// SDR file forever.
		part.HDRFormat = cmd.HDRFormat
		if cmd.SizeBytes > 0 {
			part.SizeBytes = cmd.SizeBytes
		}

		attributes, err := mergeAttribute(part.Attributes, ProbeAttribute, cmd.Probe)
		if err != nil {
			return err
		}
		part.Attributes = attributes
		part.UpdatedAt = s.clock.Now()

		updated, err := tx.Parts().Update(ctx, part)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "content.part.probed", []byte(string(updated.ID)), string(az.userID)),
		}); err != nil {
			return err
		}

		result = RecordPartProbeResult{Part: updated}
		return nil
	})
	if err != nil {
		return RecordPartProbeResult{}, err
	}

	return result, nil
}

// mergeAttribute sets one key in an attributes document, preserving the rest.
//
// Merging rather than replacing, even though nothing else writes Part attributes
// today. Attributes are the open extension point of the content model
// (platform#9), and a second writer silently erasing the first shows up as
// missing data long after the change that caused it.
func mergeAttribute(existing []byte, key string, value []byte) ([]byte, error) {
	doc := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			// An attributes document that is not an object is not something to
			// repair by guessing. Refusing keeps whatever is there, at the cost
			// of not caching this probe.
			return nil, contracts.NewError(contracts.Conflict, "part attributes are not a JSON object")
		}
	}
	if len(value) == 0 {
		delete(doc, key)
	} else {
		doc[key] = json.RawMessage(value)
	}
	if len(doc) == 0 {
		return nil, nil
	}
	return json.Marshal(doc)
}
