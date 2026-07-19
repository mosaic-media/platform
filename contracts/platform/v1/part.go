package v1

import "time"

// PartRole distinguishes the two reasons an item Node has more than one
// Part (ADR 0013). Both go through one source-selection path rather than
// two, which is the point of modelling them the same way.
type PartRole string

const (
	// PartEdition is a complete rendering of the item — a theatrical cut, a
	// director's cut, a remaster. An edition is deliberately *not* a new
	// Node: Blade Runner 2049 is one Item however many cuts exist, because
	// the cut is a property of which bytes play. An item with a single
	// unremarkable file uses this role with an empty EditionLabel.
	PartEdition PartRole = "edition"
	// PartSegment is one piece of an item that spans several files — disc 2
	// of a multi-disc release. Segments of one edition are ordered by
	// NaturalOrder.
	PartSegment PartRole = "segment"
)

// LocationScheme says whether a Part's bytes sit on a local filesystem or
// behind a remote provider. ADR 0014 makes both first-class: a library may
// be entirely local, entirely remote, or mixed, and nothing above the Part
// cares which.
type LocationScheme string

const (
	// LocalLocation is a path on a filesystem the Platform can reach.
	LocalLocation LocationScheme = "local"
	// RemoteLocation is a reference resolved through a provider, such as a
	// debrid service.
	RemoteLocation LocationScheme = "remote"
)

// MediaLocation points at bytes. It never contains them: ADR 0014 is
// explicit that media is linked, never absorbed, and that primary media is
// never rewritten, re-containered or moved into a content-addressed store.
// It stays as whatever it already is, wherever the source keeps it, so any
// standard player can direct-play it without Mosaic in the path.
type MediaLocation struct {
	Scheme LocationScheme
	// Provider names the resolving service for a RemoteLocation and is
	// empty for a LocalLocation.
	Provider string
	// Ref is a filesystem path or a provider-specific reference.
	Ref string
}

// Part is what actually gets played, attached to an item Node (ADR 0013).
// It carries technical metadata and a pointer to the bytes.
type Part struct {
	ID PartID
	// NodeID is the item Node this Part belongs to. The database enforces
	// that the target is an item and not a work or container.
	NodeID NodeID
	Role   PartRole
	// EditionLabel names the cut — "Director's Cut", "Remastered". Empty
	// means an unremarkable single file.
	EditionLabel string
	// NaturalOrder sorts Parts sharing a role and label, which is how the
	// discs of a multi-disc release stay in sequence. Same float rationale
	// as Node.NaturalOrder.
	NaturalOrder float64
	Location     MediaLocation

	// Technical metadata. Every field is optional — the zero value means
	// "not known", which is the normal state before a probe has run, and
	// applies wholesale to formats where a field is meaningless (a CBZ has
	// no audio codec).
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	// HDRFormat is empty for standard dynamic range.
	HDRFormat  string
	Duration   time.Duration
	BitrateBPS int64
	// SizeBytes is the size of the pointed-at bytes, 0 when unknown.
	SizeBytes int64

	// Attributes is a raw JSON document carrying per-format variation, on
	// the same unvalidated terms as Node.Attributes.
	Attributes []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Local reports whether the Part's bytes sit on a local filesystem.
func (p Part) Local() bool { return p.Location.Scheme == LocalLocation }
