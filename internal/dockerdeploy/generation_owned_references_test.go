package dockerdeploy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	distref "github.com/distribution/reference"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
)

func ownedReferenceGenerationFixtureV1(t *testing.T, dir, environment string, lock deploy.BuildLockV1) deploy.EnvironmentGenerationState {
	t.Helper()
	references, err := newEnvironmentImageReferences(environment, dir, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := deploy.BuildLockDigestV1(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	return deploy.EnvironmentGenerationState{Reference: references.Generation, ImageDigest: lock.FinalImage.Digest, RootFSSubject: lock.FinalImage.RootFSSubject, BuildLockDigest: digest, Platform: lock.Platform, RuntimePolicyDigest: policy}
}

func portableOwnedLockFixtureV1(t *testing.T) deploy.BuildLockV1 {
	t.Helper()
	lock, _ := portableToolReuseBuildLocksV1(t)
	result := providers.RealizedImageV1{Digest: rendererDigest("c"), ConfigDigest: rendererDigest("c"), RootFSSubject: rendererDigest("d")}
	transaction, err := deploy.PortableRuntimeLayerTransactionDigestV1(*lock.PortableTools, lock.RuntimeLayer.Upstream, result)
	if err != nil {
		t.Fatal(err)
	}
	lock.PortableRuntimeLayer = &deploy.PortableRuntimeLayerV1{Schema: deploy.PortableRuntimeLayerSchemaV1, Upstream: lock.RuntimeLayer.Upstream, Result: result, TransactionDigest: transaction}
	lock.RuntimeLayer.Upstream = result
	lock.RuntimeLayer.TransactionDigest, err = deploy.ApplicationRuntimeLayerTransactionDigestV1(lock.RuntimeLayer.Verifier, lock.RuntimeLayer.Account, result, lock.Platform)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestProjectEnvironmentOwnedReferencesV1(t *testing.T) {
	for _, portable := range []bool{false, true} {
		t.Run(fmt.Sprint(portable), func(t *testing.T) {
			dir := t.TempDir()
			_, lock := publicationLockFixture(t, dir, "a", "b", "c")
			if portable {
				lock = portableOwnedLockFixtureV1(t)
			}
			generation := ownedReferenceGenerationFixtureV1(t, dir, "demo", lock)
			pairs, err := ProjectEnvironmentOwnedReferencesV1(generation, lock, "demo", dir)
			if err != nil {
				t.Fatal(err)
			}
			want := []OwnedImageReferenceV1{{Reference: generation.Reference, Image: lock.FinalImage}}
			if portable {
				canonicalDir, err := filepath.EvalSymlinks(dir)
				if err != nil {
					t.Fatal(err)
				}
				pathHash := sha256.Sum256([]byte(canonicalDir))
				environmentHash := sha256.Sum256([]byte("demo"))
				reference := fmt.Sprintf("reploy/portable/%x/%x:g-%s", pathHash, environmentHash, strings.Repeat("01", 16))
				want = append(want, OwnedImageReferenceV1{Reference: reference, Image: lock.PortableRuntimeLayer.Result})
			}
			if !reflect.DeepEqual(pairs, want) {
				t.Fatalf("pairs = %#v, want %#v", pairs, want)
			}
			again, err := ProjectEnvironmentOwnedReferencesV1(generation, lock, "demo", dir)
			if err != nil || !reflect.DeepEqual(again, pairs) {
				t.Fatalf("projection is not deterministic: %#v, %v", again, err)
			}
		})
	}
}

func TestProjectEnvironmentOwnedReferencesRejectsUnacceptedOwnerV1(t *testing.T) {
	dir := t.TempDir()
	lock := portableOwnedLockFixtureV1(t)
	generation := ownedReferenceGenerationFixtureV1(t, dir, "demo", lock)
	for _, test := range []struct {
		name   string
		mutate func(*deploy.EnvironmentGenerationState, *deploy.BuildLockV1, *string, *string)
	}{
		{"malformed owner", func(g *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, _, _ *string) { g.Reference = "" }},
		{"wrong image", func(g *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, _, _ *string) {
			g.ImageDigest = rendererDigest("e")
		}},
		{"wrong rootfs", func(g *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, _, _ *string) {
			g.RootFSSubject = rendererDigest("e")
		}},
		{"wrong platform", func(g *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, _, _ *string) {
			g.Platform, _ = blueprint.ParsePlatform("linux/arm64")
		}},
		{"wrong lock", func(g *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, _, _ *string) {
			g.BuildLockDigest = rendererDigest("e")
		}},
		{"wrong policy", func(g *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, _, _ *string) {
			g.RuntimePolicyDigest = rendererDigest("e")
		}},
		{"wrong deployment", func(_ *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, _ *string, d *string) {
			*d = t.TempDir()
		}},
		{"wrong environment", func(_ *deploy.EnvironmentGenerationState, _ *deploy.BuildLockV1, e, _ *string) { *e = "other" }},
		{"invalid layer", func(_ *deploy.EnvironmentGenerationState, l *deploy.BuildLockV1, _, _ *string) {
			layer := *l.PortableRuntimeLayer
			layer.TransactionDigest = rendererDigest("e")
			l.PortableRuntimeLayer = &layer
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, l, environment, deploymentDir := generation, lock, "demo", dir
			test.mutate(&g, &l, &environment, &deploymentDir)
			if pairs, err := ProjectEnvironmentOwnedReferencesV1(g, l, environment, deploymentDir); err == nil || pairs != nil {
				t.Fatalf("accepted invalid authority: %#v, %v", pairs, err)
			}
		})
	}
}

func TestPortableReferencesSeparateExactScopeAndIndependentOwnersV1(t *testing.T) {
	dir := t.TempDir()
	first, err := newEnvironmentImageReferences("Demo.a", dir, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := newEnvironmentImageReferences("demo-a", dir, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != second.Generation {
		t.Fatal("fixture no longer exercises the legacy slug collision")
	}
	a, err := portableGenerationReferenceV1(first.Generation, "Demo.a", dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := portableGenerationReferenceV1(second.Generation, "demo-a", dir)
	if err != nil {
		t.Fatal(err)
	}
	other := first.Generation[:len(first.Generation)-32] + strings.Repeat("02", 16)
	c, err := portableGenerationReferenceV1(other, "Demo.a", dir)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a == c || a == first.Generation {
		t.Fatal("portable role, exact environment or generation owners collided")
	}
	// A lock binds exact environment spelling even where the primary slug does not.
	lock := portableOwnedLockFixtureV1(t)
	lock.PackageOverrides.EnvironmentID = "Demo.a"
	owner := ownedReferenceGenerationFixtureV1(t, dir, "Demo.a", lock)
	if _, err := ProjectEnvironmentOwnedReferencesV1(owner, lock, "demo-a", dir); err == nil {
		t.Fatal("projection accepted another exact environment with the same legacy slug")
	}
}

func TestPortableReferencesSeparateLegacyPathPrefixCollisionV1(t *testing.T) {
	root, err := canonicalIdentityPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Deterministically scan absent child paths under this canonical root. The
	// fixed ceiling keeps the fixture bounded while making a 32-bit-prefix
	// collision overwhelmingly likely; the selected paths are then checked by
	// the actual canonical-path and legacy-reference helpers below.
	const candidateLimit = 500_000
	seen := make(map[string]int, candidateLimit)
	var firstDir, secondDir string
	var firstFull, secondFull [sha256.Size]byte
	for index := 0; index < candidateLimit; index++ {
		candidate := filepath.Join(root, fmt.Sprintf("legacy-collision-%08x", index))
		full := sha256.Sum256([]byte(candidate))
		prefix := string(full[:4])
		previous, ok := seen[prefix]
		if !ok {
			seen[prefix] = index
			continue
		}
		prior := filepath.Join(root, fmt.Sprintf("legacy-collision-%08x", previous))
		priorFull := sha256.Sum256([]byte(prior))
		if priorFull != full {
			firstDir, secondDir = prior, candidate
			firstFull, secondFull = priorFull, full
			break
		}
	}
	if firstDir == "" {
		t.Fatalf("did not find a legacy 8-hex path-prefix collision within %d candidates", candidateLimit)
	}

	firstCanonical, err := canonicalIdentityPath(firstDir)
	if err != nil {
		t.Fatal(err)
	}
	secondCanonical, err := canonicalIdentityPath(secondDir)
	if err != nil {
		t.Fatal(err)
	}
	if firstCanonical == secondCanonical {
		t.Fatal("collision fixture did not produce distinct canonical deployment paths")
	}
	firstLegacyHash, err := pathIdentityHash(firstDir)
	if err != nil {
		t.Fatal(err)
	}
	secondLegacyHash, err := pathIdentityHash(secondDir)
	if err != nil {
		t.Fatal(err)
	}
	if firstLegacyHash != secondLegacyHash || firstLegacyHash != fmt.Sprintf("%x", firstFull[:4]) {
		t.Fatalf("fixture does not prove equal legacy prefixes: %q, %q", firstLegacyHash, secondLegacyHash)
	}
	if firstFull == secondFull || firstFull != sha256.Sum256([]byte(firstCanonical)) || secondFull != sha256.Sum256([]byte(secondCanonical)) {
		t.Fatal("fixture does not prove distinct full hashes of the canonical paths")
	}

	firstReferences, err := newEnvironmentImageReferences("demo", firstDir, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	secondReferences, err := newEnvironmentImageReferences("demo", secondDir, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if firstReferences.Generation != secondReferences.Generation {
		t.Fatalf("fixture no longer collides in the legacy generation namespace: %q, %q", firstReferences.Generation, secondReferences.Generation)
	}
	firstCompanion, err := portableGenerationReferenceV1(firstReferences.Generation, "demo", firstDir)
	if err != nil {
		t.Fatal(err)
	}
	secondCompanion, err := portableGenerationReferenceV1(secondReferences.Generation, "demo", secondDir)
	if err != nil {
		t.Fatal(err)
	}
	if firstCompanion == secondCompanion {
		t.Fatal("full-hash portable companions collided for distinct canonical deployment paths")
	}
}

func TestPortableReferencesSeparateAndBoundLongEnvironmentIDV1(t *testing.T) {
	dir := t.TempDir()
	environment := "long-" + strings.Repeat("a", 512)
	if err := blueprint.ValidateEnvironmentID("long test environment", environment); err != nil {
		t.Fatalf("long fixture environment is invalid: %v", err)
	}
	lock := portableOwnedLockFixtureV1(t)
	lock.PackageOverrides.EnvironmentID = environment
	generation := ownedReferenceGenerationFixtureV1(t, dir, environment, lock)
	pairs, err := ProjectEnvironmentOwnedReferencesV1(generation, lock, environment, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 {
		t.Fatalf("owned pairs = %#v, want primary and portable companion", pairs)
	}
	companion := pairs[1].Reference
	named, err := distref.ParseNormalizedNamed(companion)
	if err != nil {
		t.Fatalf("long environment produced an invalid complete Docker name %q: %v", companion, err)
	}
	if len(named.Name()) > distref.NameTotalLengthMax {
		t.Fatalf("companion repository name exceeds Docker's bound: %d", len(named.Name()))
	}
	tagged, ok := named.(distref.NamedTagged)
	if !ok || len(tagged.Tag()) > 128 || len(companion) > distref.NameTotalLengthMax+1+128 {
		t.Fatalf("companion Docker name is not complete and bounded: %q", companion)
	}
}
