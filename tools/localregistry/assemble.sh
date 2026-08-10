#!/usr/bin/env bash
# Assemble and sign a module index from the sibling extension-module checkouts
# (platform#55) — the local counterpart of the registry repository's
# `scripts/assemble.sh`, which does the same job against published releases.
#
# **Read that one first; this is a deliberate near-copy of it.** The two differ
# in exactly one step:
#
#   registry/scripts/assemble.sh   curl the module's manifest.json from its
#                                  GitHub release, whose CI already built the
#                                  binaries and computed their digests
#   this script                    build the binary from the checkout on disk,
#                                  ask it what it is, and compute the digest here
#
# Everything after that step is the same four `modulesign` calls the real
# publisher makes — genkey, build-manifest, build-index, sign-index — because the
# point of a local registry is to exercise the real install path rather than a
# convincing imitation of it. No format is re-derived here: the digest comes from
# the function the Platform verifies with, the manifest and index are validated
# by the tool that writes them, and the index is signed. A development key signs
# a development index; nothing is unsigned and no check is skipped.
#
# It is idempotent and cheap to re-run — that is the loop it exists for. Edit a
# module, re-run this, reinstall.
#
# Inputs, all with defaults the dev stack supplies:
#
#   SERVE_DIR   directory the static file server publishes  (/registry)
#   KEY_DIR     the throwaway signing key lives here        (/keys)
#   SRC_DIR     where the sibling checkouts are mounted     (/src)
#   MODULES     checkout directory names to catalogue
#   BASE_URL    what the Platform will fetch binaries from  (http://registry)
#
# **The key never travels over the wire.** The private half stays in KEY_DIR,
# which only this service mounts; the public half is handed to the Platform as a
# *file*, out of band, exactly as the official key is handed to it by being
# compiled in. A Platform that fetched the key from the repository it
# authenticates would verify every index that repository ever served.
set -euo pipefail

SERVE_DIR=${SERVE_DIR:-/registry}
KEY_DIR=${KEY_DIR:-/keys}
SRC_DIR=${SRC_DIR:-/src}
BASE_URL=${BASE_URL:-http://registry}
MODULES=${MODULES:-"module-stremio-addons module-aiostreams module-fanart-tv"}

WORK=/work/localregistry
rm -rf "$WORK"
mkdir -p "$WORK/manifests" "$WORK/out" "$SERVE_DIR" "$KEY_DIR"

# The checkouts are bind-mounted from the host, so git sees a tree owned by
# somebody else and refuses to read it. The version string below is the only
# thing that needs git, and a refusal would otherwise stop the whole assembly
# over a label.
git config --global --add safe.directory '*' 2>/dev/null || true

# ── The workspace ───────────────────────────────────────────────────────────
# The same trick, and the same reason, as the platform service in
# docker-compose.local.yml: a `go.work` written *outside* the bind mounts, so the
# extension modules compile against the sibling SDK and contracts rather than
# against whatever versions happen to be tagged. Mid-change those tags do not
# exist yet, and without this the build fails with "unknown revision" naming a
# module sitting right there on the disk.
#
# `use` substitutes a module's source but does not stop resolution needing to
# read the go.mod of every *required* version, so each required version is also
# replaced onto the local checkout. The versions are read out of the go.mod files
# rather than written here, because a hardcoded one is wrong a commit later and
# fails in exactly the same confusing way.
export GOWORK=$WORK/go.work
export GOFLAGS=-buildvcs=false
cd "$WORK"
go work init

present=()
for repo in $MODULES; do
  if [ -f "$SRC_DIR/$repo/go.mod" ]; then
    present+=("$repo")
  else
    echo "skipping $repo: no checkout at $SRC_DIR/$repo"
  fi
done
if [ ${#present[@]} -eq 0 ]; then
  echo "::error::none of the extension-module checkouts are present under $SRC_DIR" >&2
  echo "Check them out as siblings of platform/ — see platform/CLAUDE.md." >&2
  exit 1
fi

use=("$SRC_DIR/platform" "$SRC_DIR/sdk" "$SRC_DIR/sdk/host" "$SRC_DIR/contracts")
for repo in "${present[@]}"; do use+=("$SRC_DIR/$repo"); done
go work use "${use[@]}"

# Every version of the SDK, its host harness and the contracts that anything in
# the workspace requires, pinned onto the checkout. sdk and sdk/host are separate
# module paths in one repository, hence separate directories here.
gomods=("$SRC_DIR/platform/go.mod" "$SRC_DIR/sdk/host/go.mod")
for repo in "${present[@]}"; do gomods+=("$SRC_DIR/$repo/go.mod"); done
for spec in "sdk:$SRC_DIR/sdk" "sdk/host:$SRC_DIR/sdk/host" "contracts:$SRC_DIR/contracts"; do
  path=github.com/mosaic-media/${spec%%:*}
  dir=${spec#*:}
  # Match "<path> vX.Y.Z" with an optional leading `require`, and nothing else:
  # the trailing space in the pattern is what keeps `sdk` from matching
  # `sdk/host`.
  for version in $(sed -n "s|^[[:space:]]*\(require[[:space:]]*\)\?$path[[:space:]]\+\(v[0-9][^[:space:]]*\).*|\2|p" "${gomods[@]}" | sort -u); do
    go work edit -replace "$path@$version=$dir"
  done
done

# ── The tool ────────────────────────────────────────────────────────────────
# Built from the platform checkout in the workspace, so it is the modulesign
# this Platform's own extension package defines — same digest format, same
# manifest and index schemas, same validation. A second implementation of any of
# those is the one mistake that fails silently at the publisher and surfaces as
# "signature does not verify" on the far side.
echo "building modulesign from $SRC_DIR/platform"
go build -o "$WORK/modulesign" github.com/mosaic-media/platform/tools/modulesign

# ── The key ─────────────────────────────────────────────────────────────────
# Generated once and reused. Regenerating on every run would invalidate the key
# the running Platform read at boot, and the failure — every index suddenly
# refusing to verify — reads like a bug in the signing path rather than like a
# new key.
KEY=$KEY_DIR/dev-registry.key
if [ ! -f "$KEY" ]; then
  echo "generating a throwaway development signing key"
  "$WORK/modulesign" genkey -out "$KEY"
else
  echo "reusing the development signing key at $KEY"
fi

# The SDK major is read from the harness the modules are compiled against, not
# hardcoded. It is the one compatibility number the Platform refuses an install
# over, and it is checked before anything is executed — so a wrong constant here
# would produce a module nobody can install, for a reason that names neither
# file.
SDK_MAJOR=$(sed -n 's/^const SDKMajor = \([0-9]\+\).*/\1/p' "$SRC_DIR/sdk/host/plugin.go")
if [ -z "$SDK_MAJOR" ]; then
  echo "::error::could not read SDKMajor from $SRC_DIR/sdk/host/plugin.go" >&2
  exit 1
fi

# One platform only: whatever this container — and therefore the Platform
# container beside it — runs. The release matrix cross-compiles five targets
# because it serves every host Mosaic runs on; a local index serves exactly one
# and building the other four would cost minutes per loop to publish binaries
# nothing here can execute.
GOOS_LOCAL=linux
GOARCH_LOCAL=$(go env GOARCH)
echo "building for ${GOOS_LOCAL}/${GOARCH_LOCAL}, SDK major ${SDK_MAJOR}"

for repo in "${present[@]}"; do
  src=$SRC_DIR/$repo
  echo
  echo "── $repo ──────────────────────────────────────────────"

  # The command package, found rather than assumed: every extension module has
  # exactly one, and its name is the repository's, but a module that grew a
  # second one should say so here rather than have one picked silently.
  cmds=("$src"/cmd/*/)
  if [ ${#cmds[@]} -ne 1 ] || [ ! -d "${cmds[0]}" ]; then
    echo "::error::$repo has ${#cmds[@]} command packages under cmd/; expected exactly one" >&2
    exit 1
  fi
  cmd=$(basename "${cmds[0]}")

  # A version string that cannot be mistaken for a release. The manifest's
  # version is what the catalogue card shows and what the install record pins, so
  # a local build claiming `v0.28.0` would be indistinguishable from the real one
  # in the surface and in the store. `git describe` also makes the loop visible:
  # an uncommitted edit reads `-dirty`.
  described=$(git -C "$src" describe --tags --always --dirty 2>/dev/null || echo unknown)
  version="local-$described"

  binary="$cmd-$GOOS_LOCAL-$GOARCH_LOCAL"
  # A module carrying a project credential (architecture#4) has it linked in by the
  # workflow that builds the artefact shipping it — which for an extension is
  # that module's own `release.yml`, not platform's. This loop is the local
  # counterpart of that build, so it applies the same `-X` from the same
  # bring-your-own value the dev stack uses for TMDB: `platform/.env` or the
  # shell, never a GitHub secret, which reaches only CI runners.
  #
  # Empty is a supported configuration and the interesting one to be able to
  # reach: it is what a build with no secret set produces, so the fallback chain
  # architecture#4 names — personal key, project key, zero-configuration floor — can
  # be exercised locally rather than assumed. The symbol path here is the same
  # string the module's own linkercheck gate asserts against a canary.
  ldflags=""
  case $repo in
    module-fanart-tv)
      ldflags="-X github.com/mosaic-media/module-fanart-tv.defaultAPIKey=${FANART_PROJECT_KEY:-}"
      if [ -n "${FANART_PROJECT_KEY:-}" ]; then
        echo "linking a fanart.tv project key into this build"
      else
        echo "no fanart.tv project key (set FANART_PROJECT_KEY in platform/.env, or add a key in Settings)"
      fi
      ;;
  esac

  echo "building $version"
  (cd "$src" && CGO_ENABLED=0 GOOS=$GOOS_LOCAL GOARCH=$GOARCH_LOCAL \
    go build -trimpath -ldflags "$ldflags" -o "$WORK/out/$binary" "./cmd/$cmd")

  # The module tells the manifest what it is — id, name, roles, its own sentence
  # about itself — so the identity has one source of truth, the Go code, and the
  # local index carries the same text the published one would. This is also what
  # makes a change to a module's own description visible through the loop.
  "$WORK/out/$binary" --mosaic-manifest > "$WORK/out/$cmd-identity.json"

  # The binary is served under its repository directory, so the URL needs no
  # placeholder for the module id: a module's id does not follow from its
  # repository name (module-stremio-addons publishes the module whose id is
  # "stremio"), and here the repository name is the thing this script knows.
  "$WORK/modulesign" build-manifest \
    -identity "$WORK/out/$cmd-identity.json" \
    -sdk-major "$SDK_MAJOR" \
    -version "$version" \
    -url "$BASE_URL/$repo/$cmd-{os}-{arch}{ext}" \
    -out "$WORK/manifests/$repo.json" \
    "$GOOS_LOCAL/$GOARCH_LOCAL=$WORK/out/$binary"

  mkdir -p "$SERVE_DIR/$repo"
  cp "$WORK/out/$binary" "$SERVE_DIR/$repo/$binary"
done

# ── The index ───────────────────────────────────────────────────────────────
# build-index wraps the manifests — which already carry their binaries' URLs and
# digests — into the catalogue and validates that the result parses before
# writing; sign-index signs those exact bytes. Same two calls the registry's
# publish workflow makes.
echo
"$WORK/modulesign" build-index -out "$WORK/out/index.json" "$WORK"/manifests/*.json
"$WORK/modulesign" sign-index -key "$KEY" "$WORK/out/index.json"

# Moved in together, and last. The index and its detached signature are fetched
# as two requests, so publishing them in place one at a time gives a Platform
# that catalogues between the two writes a signature for the wrong bytes — a
# verification failure with no cause anywhere near it.
mv "$WORK/out/index.json" "$WORK/out/index.json.sig" "$SERVE_DIR/"

echo
echo "published $SERVE_DIR/index.json — ${#present[@]} module(s), signed with $(basename "$KEY")"
echo "the Platform trusts it through MOSAIC_DEV_REPOSITORY_URL + MOSAIC_DEV_REPOSITORY_KEY (platform#55)"
