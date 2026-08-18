# Module storage is granted, quota-bounded, and cannot be made exclusive

**Status:** Accepted. **Not built.** Supersedes
[platform#2](0002-module-storage-and-delivery-model.md) **in part** — its rule
that modules do not own storage stands as an intent and can no longer stand as a
guarantee. Depends on
[platform#90](0090-one-origin-facility-consumers-declare-against.md) and
[platform#85](0085-a-modules-authority-is-declared-and-consented.md). Slice 7 of
the extension surface.

## Context

A DVR, offline downloads and trickplay all need a module to write bytes.
[platform#25](0025-playback-consumer-and-media-origin.md) named module scratch
storage as deferred and [platform#29](0029-probing-and-the-per-stream-playback-decision.md)
recorded the pressure building for it.

`LocationScheme` already has `LocalLocation`, and `attach_content_part.go`
already validates it. **Nothing serves it.** A Part can be recorded with local
bytes today and then not played, so what is missing is the serving half rather
than the model.

**And the rule this slice inherits is no longer enforceable.**
[platform#2](0002-module-storage-and-delivery-model.md) states that modules do
not own storage or schema, with the Platform owning the database, migrations,
transactions, access policy and the backup boundary. That was true by
construction when a module was a Go library compiled into the binary sharing the
Platform's handle. An extension module is a separate process: it can import a
SQLite driver, open a file, and allocate what it likes. Nothing in the Platform
prevents it, and nothing can.

## Decision

**Two grants, because the cases genuinely differ.** An **object API** — put, get,
delete by key, quota checked at the call — for structured output. A **per-module
directory**, created by the Platform and swept against its quota, for streaming
bulk. A DVR writing a live stream through a call per chunk is the wrong picture,
and a sync map does not need a filesystem.

**A grant is one bounded place, not a filesystem.** A module reads and writes
inside its own grant and nowhere else: not another module's, not the Platform's
data directory, not a path a user names.

**The quota is declared in the manifest and consented at install**, like every
other authority under
[platform#85](0085-a-modules-authority-is-declared-and-consented.md), and the
operator may lower it afterwards. Putting the number in front of a human while
they are deciding whether to trust the binary is the point: "this wants 500 GB"
is exactly what consent should surface.

**What a module writes can become a Part**, with `LocalLocation`, served through
the origin facility. The Part is Platform state written through `ContentService`
as everything else is; this completes a scheme the contract has carried unserved.

**On uninstall, what became content survives and scratch is reclaimed.** This
mirrors [platform#60](0060-the-library-is-built-from-rules.md), where rules add
and never remove and a rule outlives its module "degraded and visibly so". A
recording somebody made is theirs. Working files are not content. The same line
settles what [platform#89](0089-annotations-are-facts-and-documents-ordered-by-the-operator.md)
left open for annotation documents: one that fed a visible fact stays, scratch
does not.

### The part that cannot be enforced, stated as such

**The Platform cannot stop a module bringing its own storage**, and this record
will not pretend otherwise. Preventing a subprocess from opening a file needs a
read-only mount, a dedicated uid or a namespace — privileges a non-root Platform
does not have, and on macOS and Windows there is no low-cost mechanism at all.
This is exactly the position `EgressContainment` already takes for the network:
the Platform reports the posture rather than claiming the guarantee, because
[platform#50](0050-deployment-topologies.md) settled that the guarantee belongs
to the deployment.

**So storage containment is reported, not asserted**, alongside egress
containment and by the same mechanism.

**The grant is made better rather than mandatory.** Inside it, storage is
quota-counted, sits inside the Platform's backup boundary, is reclaimed on
uninstall, and can become a Part. Outside it, a module's private store is
invisible to backup, uncounted against any quota, orphaned when the module is
removed, and cannot be served. A module author who brings their own SQLite is
choosing to lose all of that, which is a stronger discouragement than a rule
nothing checks.

**The backup argument is the sharpest of those and is not yet cashable.** A
module's private store falls outside the Platform's backup boundary, so a restore
produces Platform state from after and module state from before. Mosaic has no
documented restore path at all today, so this record states the reasoning and
notes the dependency rather than claiming the protection exists.

**Resource use is observed, not capped — and is currently observed by nothing.**
No RSS, CPU, disk or handle count is collected per module anywhere in the
Platform today. Surfacing it belongs with the expert-mode diagnostics that
already exist, and until it does, "the Platform manages module resource use" is
a sentence nobody may write.

## Alternatives considered

**The object API alone.** No filesystem handle ever leaves the Platform and every
write is mediated. Rejected because bulk then crosses the process boundary per
write, which is expensive in exactly the dimension the DVR and download cases
care about.

**The directory alone.** One grant, one quota, one thing to explain. Rejected
because structured output does not want a filesystem and would invent a key-value
layout inside one.

**Deleting everything on uninstall.** No orphans and nothing to sweep. Rejected:
uninstalling a DVR would delete the recordings, which is a destructive act
disguised as housekeeping.

**Retaining everything for the operator to reclaim.** Nothing is ever lost.
Rejected: disk leaks by default and the person who would have to notice is the
least likely to look.

**Forbidding a module its own storage.** Rejected as unenforceable. A rule that
cannot be checked is the pattern this project keeps finding and removing, and
writing one here would be adding a fresh instance knowingly.

## Consequences

- **"Became content" needs a definition sharp enough to sweep on.** Too narrow
  strands disk forever; too wide deletes something a person wanted. Nothing in
  this record makes that boundary precise, and it is the first thing the
  implementation has to settle.
- **A Part can now point at bytes a module may delete.** A dangling Part becomes
  possible in a way it was not when every Part pointed at a remote location or
  Platform-owned bytes.
- **A quota swept after the fact still lets a module fill a disk between
  sweeps.** The bound is a policy, not a hard limit, and an operator on a small
  disk should read it that way.
- **This is the filesystem grant that deliberately did not exist.** It is a real
  expansion of what a module holds, and the mitigation is a bounded path plus a
  reported posture rather than an enforced boundary.
