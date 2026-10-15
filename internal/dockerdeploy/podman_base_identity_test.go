package dockerdeploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const podmanIdentityVersion = `{"Client":{"Version":"29.8.1"},"Server":{"Version":"6.1.4","Os":"linux","Platform":{"Name":"linux/amd64"},"Components":[{"Name":"Podman Engine","Version":"6.1.4"}]}}`

func podmanIdentityManifest(t *testing.T, architecture string, config string) registryManifestObservation {
	t.Helper()
	raw := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":500},"layers":[]}`, config))
	sum := sha256.Sum256(raw)
	r := registryManifestObservation{Raw: raw, Descriptor: registryManifestDescriptor{
		MediaType: "application/vnd.oci.image.manifest.v1+json", Digest: canonical.Digest("sha256:" + hex.EncodeToString(sum[:])), Size: int64(len(raw)),
	}}
	r.Ref = "docker.io/library/debian:13@" + string(r.Descriptor.Digest)
	r.Descriptor.Platform.OS, r.Descriptor.Platform.Architecture = "linux", architecture
	return r
}

func podmanIdentityJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func podmanIdentityInspection(config, manifest, architecture string) string {
	return fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q,%q],"Os":"linux","Architecture":%q,"RootFS":{"Layers":[%q]},"Config":{}}]`, config,
		"debian@sha256:"+strings.Repeat("f", 64), "debian@"+manifest, architecture, "sha256:"+strings.Repeat("3", 64))
}

func TestResolveBasePodmanMissingDigestUsesVerifiedPlatformContent(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	config := "sha256:" + strings.Repeat("1", 64)
	record := podmanIdentityManifest(t, "amd64", config)
	arm := podmanIdentityManifest(t, "arm64", "sha256:"+strings.Repeat("2", 64))
	inspection := podmanIdentityInspection(config, string(record.Descriptor.Digest), "amd64")
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch args[0] {
		case "pull":
			return "123456789abc: Download complete\n", nil
		case "version":
			return podmanIdentityVersion, nil
		case "manifest":
			return podmanIdentityJSON(t, []registryManifestObservation{arm, record}), nil
		case "image":
			return inspection, nil
		}
		t.Fatalf("unexpected command: %v", args)
		return "", nil
	}
	descriptor, _, err := resolveBase(t.Context(), "debian:13", platform, run)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.AuthorReference != "debian:13" || descriptor.ConfigDigest != canonical.Digest(config) || descriptor.ManifestDigest != record.Descriptor.Digest {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	want := [][]string{{"pull", "--platform", "linux/amd64", "debian:13"}, {"version", "--format", "{{json .}}"}, {"manifest", "inspect", "--verbose", "debian:13"}, {"image", "inspect", config}, {"image", "inspect", "debian:13"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestResolveBasePodmanRejectsContentAndTagRaces(t *testing.T) {
	for _, failure := range []string{"immutable config", "immutable platform", "tag config", "tag config projection", "manifest unavailable", "immutable unavailable", "tag unavailable", "Docker missing digest", "unknown engine"} {
		t.Run(failure, func(t *testing.T) {
			platform, _ := blueprint.ParsePlatform("linux/amd64")
			config := "sha256:" + strings.Repeat("1", 64)
			record := podmanIdentityManifest(t, "amd64", config)
			inspection := podmanIdentityInspection(config, string(record.Descriptor.Digest), "amd64")
			imageCalls := 0
			run := func(_ context.Context, args ...string) (string, error) {
				switch args[0] {
				case "pull":
					return "Download complete", nil
				case "version":
					if failure == "unknown engine" {
						return `{}`, nil
					}
					if failure == "Docker missing digest" {
						return strings.ReplaceAll(strings.ReplaceAll(podmanIdentityVersion, "Podman Engine", "Engine"), "6.1.4", "29.8.1"), nil
					}
					return podmanIdentityVersion, nil
				case "manifest":
					if failure == "manifest unavailable" {
						return "", fmt.Errorf("registry unavailable")
					}
					return podmanIdentityJSON(t, record), nil
				case "image":
					imageCalls++
					if failure == "immutable unavailable" || failure == "tag unavailable" && imageCalls == 2 {
						return "", fmt.Errorf("image unavailable")
					}
					if failure == "immutable config" && imageCalls == 1 || failure == "tag config" && imageCalls == 2 {
						return strings.ReplaceAll(inspection, config, "sha256:"+strings.Repeat("9", 64)), nil
					}
					if failure == "immutable platform" && imageCalls == 1 {
						return strings.ReplaceAll(inspection, "amd64", "arm64"), nil
					}
					if failure == "tag config projection" && imageCalls == 2 {
						return strings.ReplaceAll(inspection, `"Config":{}`, `"Config":{"User":"999"}`), nil
					}
					return inspection, nil
				}
				t.Fatalf("unexpected command: %v", args)
				return "", nil
			}
			if _, _, err := resolveBase(t.Context(), "debian:13", platform, run); err == nil {
				t.Fatal("invalid base identity accepted")
			}
			if (failure == "Docker missing digest" || failure == "unknown engine") && imageCalls != 0 {
				t.Fatal("unidentified compatibility fallback reached image inspection")
			}
		})
	}
}

func TestPodmanRegistryBaseIdentityRejectsMalformedOrConflictingEvidence(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	for _, failure := range []string{"empty", "invalid JSON", "empty list", "other repository", "ref digest", "raw content", "size", "config digest", "config media", "index in place of image", "wrong platform", "conflicting platform", "missing index platform", "pinned mismatch"} {
		t.Run(failure, func(t *testing.T) {
			record := podmanIdentityManifest(t, "amd64", "sha256:"+strings.Repeat("1", 64))
			author := "debian:13"
			var data string
			switch failure {
			case "empty":
				data = " "
			case "invalid JSON":
				data = "["
			case "empty list":
				data = "[]"
			case "other repository":
				record.Ref = strings.ReplaceAll(record.Ref, "debian", "alpine")
			case "ref digest":
				record.Ref = "debian@sha256:" + strings.Repeat("9", 64)
			case "raw content":
				record.Raw = append(record.Raw, ' ')
				record.Descriptor.Size++
			case "size":
				record.Descriptor.Size++
			case "config digest":
				record = podmanIdentityManifest(t, "amd64", "bad")
			case "config media", "index in place of image":
				if failure == "config media" {
					record.Raw = []byte(strings.ReplaceAll(string(record.Raw), "application/vnd.oci.image.config.v1+json", "unknown"))
				} else {
					record.Raw = []byte(strings.ReplaceAll(string(record.Raw), "application/vnd.oci.image.manifest.v1+json", "application/vnd.oci.image.index.v1+json"))
					record.Descriptor.MediaType = "application/vnd.oci.image.index.v1+json"
				}
				sum := sha256.Sum256(record.Raw)
				record.Descriptor.Digest = canonical.Digest("sha256:" + hex.EncodeToString(sum[:]))
				record.Descriptor.Size = int64(len(record.Raw))
				record.Ref = "debian@" + string(record.Descriptor.Digest)
			case "wrong platform":
				record.Descriptor.Platform.Architecture = "arm64"
			case "conflicting platform":
				other := podmanIdentityManifest(t, "amd64", "sha256:"+strings.Repeat("2", 64))
				data = podmanIdentityJSON(t, []registryManifestObservation{record, other})
			case "missing index platform":
				record.Descriptor.Platform.OS = ""
				data = podmanIdentityJSON(t, []registryManifestObservation{record})
			case "pinned mismatch":
				author = "debian@sha256:" + strings.Repeat("f", 64)
			}
			if data == "" && failure != "empty" {
				data = podmanIdentityJSON(t, record)
			}
			if _, _, err := podmanRegistryBaseIdentity(author, platform, []byte(data)); err == nil {
				t.Fatal("invalid registry evidence accepted")
			}
		})
	}
}
