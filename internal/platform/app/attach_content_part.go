package app

import (
	"context"
	"time"

	v1 "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// AttachContentPartCommand attaches playable bytes to an item node (ADR
// 0013). A Part points at bytes and never contains them (ADR 0014), so the
// command carries a location, not media.
type AttachContentPartCommand struct {
	CallerSessionID domain.SessionID
	// NodeID must be an item. A work or container has nothing to play, and
	// the store enforces this, but the command checks first for a clearer
	// error and to confirm the node exists.
	NodeID v1.NodeID
	Role   v1.PartRole
	// EditionLabel names the cut — empty for an unremarkable single file.
	EditionLabel string
	NaturalOrder float64
	Location     v1.MediaLocation
	// Technical metadata is optional; the zero value means "not known".
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	HDRFormat  string
	Duration   time.Duration
	BitrateBPS int64
	SizeBytes  int64
	Attributes []byte
}

// AttachContentPartResult carries the committed part.
type AttachContentPartResult struct {
	Part v1.Part
}

func validateAttachContentPartCommand(cmd AttachContentPartCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.NodeID == "" {
		return contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	if cmd.Role != v1.PartEdition && cmd.Role != v1.PartSegment {
		return contracts.NewError(contracts.InvalidArgument, "part role must be edition or segment")
	}
	switch cmd.Location.Scheme {
	case v1.LocalLocation:
		if cmd.Location.Provider != "" {
			return contracts.NewError(contracts.InvalidArgument, "a local location has no provider")
		}
	case v1.RemoteLocation:
		if cmd.Location.Provider == "" {
			return contracts.NewError(contracts.InvalidArgument, "a remote location requires a provider")
		}
	default:
		return contracts.NewError(contracts.InvalidArgument, "location scheme must be local or remote")
	}
	if cmd.Location.Ref == "" {
		return contracts.NewError(contracts.InvalidArgument, "location reference is required")
	}
	return nil
}

// AttachContentPart adds one playable rendering to an item.
func (s *Service) AttachContentPart(ctx context.Context, cmd AttachContentPartCommand) (AttachContentPartResult, error) {
	// 1. validate command shape.
	if err := validateAttachContentPartCommand(cmd); err != nil {
		return AttachContentPartResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return AttachContentPartResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentCreate, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return AttachContentPartResult{}, err
	}

	var result AttachContentPartResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5-6. a Part is what plays, and only an item plays. Rejecting a work
		// or container here gives a clearer error than the store's foreign
		// key, and confirms the node exists.
		node, err := tx.Nodes().FindByID(ctx, cmd.NodeID)
		if err != nil {
			return err
		}
		if node.Kind != v1.NodeItem {
			return contracts.NewError(contracts.InvalidArgument, "a part can only attach to an item")
		}

		now := s.clock.Now()
		part := v1.Part{
			ID:           v1.PartID(s.contentIDs.NewID()),
			NodeID:       cmd.NodeID,
			Role:         cmd.Role,
			EditionLabel: cmd.EditionLabel,
			NaturalOrder: cmd.NaturalOrder,
			Location:     cmd.Location,
			Container:    cmd.Container,
			VideoCodec:   cmd.VideoCodec,
			AudioCodec:   cmd.AudioCodec,
			Width:        cmd.Width,
			Height:       cmd.Height,
			HDRFormat:    cmd.HDRFormat,
			Duration:     cmd.Duration,
			BitrateBPS:   cmd.BitrateBPS,
			SizeBytes:    cmd.SizeBytes,
			Attributes:   cmd.Attributes,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		// 7. persist state and the outbox event in the same transaction.
		created, err := tx.Parts().Create(ctx, part)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("content.part.attached", []byte(string(created.ID)), string(callerID)),
		}); err != nil {
			return err
		}

		result = AttachContentPartResult{Part: created}
		return nil
	})
	if err != nil {
		return AttachContentPartResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
