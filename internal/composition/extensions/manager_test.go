// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extensions_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
	"github.com/mosaic-media/platform/internal/composition/extensions"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// The Manager is what makes the installed set real at runtime. This exercises the
// whole of it against a genuinely spawned module: an install verifies, spawns and
// records; the module then resolves through the capability registry exactly as a
// compiled-in one would; and an uninstall stops it, makes it unresolvable, and
// drops the record. It is the runtime half of ADR 0081, proven without the SDUI
// surface that will eventually drive it.
func TestManagerInstallMakesAModuleResolvableAndUninstallRemovesIt(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	store := newFakeStore()
	dir := t.TempDir()

	mgr := extensions.NewManager(extensions.Deps{
		Installer: newProbeInstaller(t, dir),
		Registry:  reg,
		Store:     store,
		Content:   stubContent{},
		Clock:     fakeClock{},
		Policy:    extension.DefaultRestartPolicy(),
		Root:      discardLogger(),
	})
	t.Cleanup(mgr.Close)

	ctx := context.Background()
	rec, err := mgr.Install(ctx, "mosaic-official", "extprobe")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rec.ModuleID != "extprobe" || rec.Version != "v0.1.0" {
		t.Errorf("install record: got %+v", rec)
	}
	if rec.Repository != "mosaic-official" || rec.SignedBy != "mosaic-official" {
		t.Errorf("provenance not kept: got %+v", rec)
	}

	// The module now resolves through the registry the rest of the Platform reads,
	// with no marker that it is out of process.
	if _, ok := reg.Lookup("extprobe"); !ok {
		t.Error("installed module is not resolvable in the registry")
	}
	if got := reg.SearchProviders(); len(got) != 1 || got[0].ModuleID != "extprobe" {
		t.Errorf("installed module is not enumerated as a search provider: %+v", got)
	}
	if store.count() != 1 {
		t.Errorf("install did not persist a record: store has %d", store.count())
	}
	if live := mgr.Installed(); len(live) != 1 || live[0] != "extprobe" {
		t.Errorf("manager does not report the module live: %v", live)
	}

	// Uninstall stops it and makes it unresolvable everywhere, and drops the
	// record so a later boot does not bring it back.
	if err := mgr.Uninstall(ctx, "extprobe"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := reg.Lookup("extprobe"); ok {
		t.Error("uninstalled module still resolves in the registry")
	}
	if got := reg.SearchProviders(); len(got) != 0 {
		t.Errorf("uninstalled module is still enumerated: %+v", got)
	}
	if store.count() != 0 {
		t.Errorf("uninstall did not drop the record: store has %d", store.count())
	}
	if live := mgr.Installed(); len(live) != 0 {
		t.Errorf("manager still reports the module live after uninstall: %v", live)
	}

	// Uninstalling again is idempotent.
	if err := mgr.Uninstall(ctx, "extprobe"); err != nil {
		t.Errorf("second uninstall should be a no-op, got: %v", err)
	}
}

// A restart reconstructs the running set from the durable record and the on-disk
// cache (ADR 0081): a second Manager over the same store and install directory,
// with a fresh registry, re-adopts what the first installed — without the
// repository being reached again.
func TestManagerReAdoptsTheInstalledSetAcrossARestart(t *testing.T) {
	store := newFakeStore()
	dir := t.TempDir()
	installer := newProbeInstaller(t, dir)

	// First run: install, which persists a record and caches the binary on disk.
	first := extensions.NewManager(extensions.Deps{
		Installer: installer, Registry: app.NewCapabilityRegistry(), Store: store,
		Content: stubContent{}, Clock: fakeClock{},
		Policy: extension.DefaultRestartPolicy(), Root: discardLogger(),
	})
	ctx := context.Background()
	if _, err := first.Install(ctx, "mosaic-official", "extprobe"); err != nil {
		t.Fatalf("install: %v", err)
	}
	first.Close() // the "shutdown" — the first process goes away.

	// Second run: a fresh registry and Manager, same store and install dir. Boot
	// re-adoption brings the module back.
	reg2 := app.NewCapabilityRegistry()
	second := extensions.NewManager(extensions.Deps{
		Installer: installer, Registry: reg2, Store: store,
		Content: stubContent{}, Clock: fakeClock{},
		Policy: extension.DefaultRestartPolicy(), Root: discardLogger(),
	})
	t.Cleanup(second.Close)

	if err := second.AdoptInstalled(ctx); err != nil {
		t.Fatalf("adopt installed: %v", err)
	}
	if _, ok := reg2.Lookup("extprobe"); !ok {
		t.Error("the installed module was not re-adopted into the fresh registry after restart")
	}
	if live := second.Installed(); len(live) != 1 || live[0] != "extprobe" {
		t.Errorf("manager does not report the re-adopted module live: %v", live)
	}
}

// --- test doubles -----------------------------------------------------------

func discardLogger() *telemetry.Logger {
	return telemetry.New(telemetry.NewJSONSink(io.Discard), telemetry.Resource{}, telemetry.LevelError)
}

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// stubContent is the module's callback target. The probe never calls it in these
// tests, so it only needs to satisfy the interface.
type stubContent struct{ v1.ContentService }

// fakeStore is an in-memory InstalledExtensionStore keyed by module id.
type fakeStore struct {
	mu   sync.Mutex
	byID map[string]domain.InstalledExtension
}

func newFakeStore() *fakeStore {
	return &fakeStore{byID: make(map[string]domain.InstalledExtension)}
}

func (s *fakeStore) List(context.Context) ([]domain.InstalledExtension, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.InstalledExtension, 0, len(s.byID))
	for _, v := range s.byID {
		out = append(out, v)
	}
	return out, nil
}

func (s *fakeStore) Upsert(_ context.Context, e domain.InstalledExtension) (domain.InstalledExtension, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[e.ModuleID] = e
	return e, nil
}

func (s *fakeStore) Remove(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
	return nil
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

// newProbeInstaller builds a real Installer whose one repository serves the
// extprobe binary under a signed index, storing installs under dir. It is the
// installer the Manager drives, with a fake fetcher standing in for the network
// so the test is hermetic while everything past the fetch — verify, digest,
// spawn — is real.
func newProbeInstaller(t *testing.T, dir string) *extension.Installer {
	t.Helper()

	probe := buildProbe(t)
	binaryData, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("reading probe: %v", err)
	}
	digest := digestOf(t, probe)

	const base = "https://repo.invalid/mosaic"
	binURL := base + "/modules/extprobe/module"
	index := map[string]any{
		"schema": extension.IndexSchema,
		"modules": []map[string]any{{
			"manifest": map[string]any{
				"schema": extension.ManifestSchema, "id": "extprobe", "version": "v0.1.0",
				"name": "Extension Probe", "sdk_major": 0,
				"provides": []string{string(v1.RoleSearch)},
				"binaries": []map[string]string{
					{"os": runtime.GOOS, "arch": runtime.GOARCH, "digest": digest, "url": binURL},
				},
			},
		}},
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshalling index: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	fetch := &fakeFetcher{files: map[string][]byte{
		base + "/index.json":     indexBytes,
		base + "/index.json.sig": ed25519.Sign(priv, indexBytes),
		binURL:                   binaryData,
	}}
	reg := extension.NewRegistry()
	if err := reg.Add(extension.Repository{Name: "mosaic-official", URL: base, Key: pub, Official: true}); err != nil {
		t.Fatalf("adding repo: %v", err)
	}
	return extension.NewInstaller(reg, fetch, dir)
}

type fakeFetcher struct{ files map[string][]byte }

func (f *fakeFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	b, ok := f.files[url]
	if !ok {
		return nil, fmt.Errorf("fake fetcher: no %s", url)
	}
	return b, nil
}

func buildProbe(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "extprobe")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/mosaic-media/platform/test/extprobe")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test/extprobe: %v\n%s", err, out)
	}
	return bin
}

func digestOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading binary: %v", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
