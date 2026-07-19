package domain

import (
	"strings"
	"time"
)

// NormaliseTypeName reduces a media, container or item type to canonical
// form: lowercase, with any run of separators collapsed to a single
// underscore.
//
// The open vocabularies (ADR 0015) are unconstrained text, so "Anime Series",
// "anime-series" and "anime_series" would otherwise be three distinct types
// browsing as three separate libraries. Normalising collapses them to one.
// It does not — and cannot — catch "animeseries" or a genuine misspelling;
// those need the media_types registry, which lands with the reference
// capability.
//
// A colon is preserved so a future module-supplied type can namespace itself
// ("animekit:ova") without the normaliser flattening the separator.
func NormaliseTypeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	pendingSeparator := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			r += 'a' - 'A'
			fallthrough
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ':':
			// A separator only becomes an underscore once something follows
			// it, which drops leading and trailing runs without a second pass.
			if pendingSeparator && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingSeparator = false
			b.WriteRune(r)
		default:
			// Spaces, hyphens, underscores and anything else all separate.
			// Mapping the unrecognised to a separator rather than dropping it
			// keeps "sci-fi/anime" as sci_fi_anime instead of sci_fianime.
			pendingSeparator = true
		}
	}
	return b.String()
}

// NormaliseMediaType is NormaliseTypeName in the MediaType type, for
// capabilities deriving a type from a provider's own vocabulary.
func NormaliseMediaType(s string) MediaType { return MediaType(NormaliseTypeName(s)) }

// NormaliseContainerType is NormaliseTypeName in the ContainerType type.
func NormaliseContainerType(s string) ContainerType { return ContainerType(NormaliseTypeName(s)) }

// NormaliseItemType is NormaliseTypeName in the ItemType type.
func NormaliseItemType(s string) ItemType { return ItemType(NormaliseTypeName(s)) }

// NodeKind is a Node's structural role in the containment tree (ADR 0013).
// It is closed and Platform-owned: the tree has exactly these three roles,
// and code that traverses the tree relies on them.
type NodeKind string

const (
	// NodeWork is the root of a containment tree — a film, a series, an
	// album, an artist, a collection. A Work never has a parent.
	NodeWork NodeKind = "work"
	// NodeContainer is an intermediate grouping such as a season, a volume
	// or a disc. Containers may nest.
	NodeContainer NodeKind = "container"
	// NodeItem is a leaf that Parts attach to — an episode, a track, a
	// chapter, a feature.
	NodeItem NodeKind = "item"
)

// MediaType names the kind of thing a Node is — movie, tv_series,
// anime_series, album, book, manga_series, comic_series, podcast,
// iptv_channel, collection, artist and whatever comes next.
//
// It is deliberately open (ADR 0015). The Platform never branches on a
// media type, so it is descriptive rather than structural, and constraining
// it to a fixed list would make every new media type a schema migration —
// the outcome ADR 0013 exists to prevent. The database stores it as
// unconstrained text.
//
// Nothing validates it, which means a typo fragments a library silently:
// anime_series and anime-series browse as two separate things. Use these
// constants rather than string literals. A media_types registry is the
// anticipated fix, due when something other than Platform code can
// introduce a type.
type MediaType string

// The media types named in ADR 0013, provided as constants for the
// Platform's own use. The list is a starting vocabulary, not a closed set —
// see MediaType.
const (
	MediaMovie       MediaType = "movie"
	MediaTVSeries    MediaType = "tv_series"
	MediaAnimeSeries MediaType = "anime_series"
	MediaAlbum       MediaType = "album"
	MediaBook        MediaType = "book"
	MediaMangaSeries MediaType = "manga_series"
	MediaComicSeries MediaType = "comic_series"
	MediaPodcast     MediaType = "podcast"
	MediaIPTVChannel MediaType = "iptv_channel"
	MediaCollection  MediaType = "collection"
)

// ContainerType names what kind of grouping a container Node is. Open for
// the same reason as MediaType.
type ContainerType string

// The container types named in ADR 0013.
const (
	ContainerSeason ContainerType = "season"
	ContainerVolume ContainerType = "volume"
	ContainerArc    ContainerType = "arc"
	ContainerDisc   ContainerType = "disc"
	ContainerBoxSet ContainerType = "box_set"
)

// ItemType names what kind of leaf an item Node is. Open for the same
// reason as MediaType.
type ItemType string

// The item types named in ADR 0013.
const (
	ItemEpisode ItemType = "episode"
	ItemTrack   ItemType = "track"
	ItemChapter ItemType = "chapter"
	ItemIssue   ItemType = "issue"
	ItemFeature ItemType = "feature"
	ItemExtra   ItemType = "extra"
)

// NodeStatus records whether a Node still has a source behind it.
type NodeStatus string

const (
	// NodeActive is a Node with at least one SourceBinding.
	NodeActive NodeStatus = "active"
	// NodeOrphaned is a Node whose last binding was removed. ADR 0013 is
	// explicit that this is not deletion: removing the last source leaves
	// the Node standing, and deleting it is a decision a user confirms.
	NodeOrphaned NodeStatus = "orphaned"
)

// Node is one position in the containment tree (ADR 0013). Depth is
// whatever a given work's real structure needs: a film is Work → Item, a
// series is Work → Container(season) → Item(episode), and a chapter-only
// manga is Work → Item until a volume layer is inserted later.
//
// Nothing may assume a Node has a parent, or that a Work's children are
// containers. Every traversal goes through ParentID.
type Node struct {
	ID NodeID
	// WorkID is the root Work of this node's tree. For a Work it is the
	// node's own ID. It is denormalised so that "everything belonging to
	// this work" is one indexed scan rather than a recursive walk.
	WorkID NodeID
	// ParentID is nil for a Work and set for everything else.
	ParentID *NodeID
	Kind     NodeKind
	// MediaType is carried on every node in a tree, not just the Work, so
	// that a node is interpretable without walking to its root.
	MediaType MediaType
	// ContainerType is empty unless Kind is NodeContainer.
	ContainerType ContainerType
	// ItemType is empty unless Kind is NodeItem.
	ItemType ItemType
	Title    string
	// NaturalOrder sorts siblings. It is a float so that 5.5 inserts
	// between 5 and 6 without renumbering the rest. ADR 0013 leaves the
	// exact fractional scheme at large scale unsettled, so the Platform
	// stores whatever value it is given and does not rebalance.
	NaturalOrder float64
	Status       NodeStatus
	// ExternalIDs maps a provider scheme to that provider's identifier —
	// {"tmdb": "329865", "anilist": "1234"} — as a raw JSON document. The
	// flat scheme-to-string shape is what NodeStore.FindByExternalID reads;
	// nesting or non-string values will store fine and simply not be found.
	ExternalIDs []byte
	// Attributes is where per-media-type variation lives instead of in
	// per-type columns.
	//
	// Neither document is validated by the schema: ADR 0013 assigns their
	// correctness to the writing capability. Both are GIN-indexed, so they
	// are queryable but not typed.
	Attributes []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Canonical returns a copy of the node with its open type vocabularies
// normalised. Stores apply this on write, so what is persisted and read back
// is canonical whichever capability wrote it.
func (n Node) Canonical() Node {
	n.MediaType = NormaliseMediaType(string(n.MediaType))
	n.ContainerType = NormaliseContainerType(string(n.ContainerType))
	n.ItemType = NormaliseItemType(string(n.ItemType))
	return n
}

// IsRoot reports whether the node is the root Work of its tree.
func (n Node) IsRoot() bool { return n.ParentID == nil }

// Orphaned reports whether the node has lost its last source binding.
func (n Node) Orphaned() bool { return n.Status == NodeOrphaned }

// MarkOrphaned returns a copy of the node with its last binding removed.
func (n Node) MarkOrphaned(now time.Time) Node {
	n.Status = NodeOrphaned
	n.UpdatedAt = now
	return n
}

// MarkActive returns a copy of the node with a source behind it again.
func (n Node) MarkActive(now time.Time) Node {
	n.Status = NodeActive
	n.UpdatedAt = now
	return n
}
