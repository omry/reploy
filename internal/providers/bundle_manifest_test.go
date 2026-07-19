package providers

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func TestResolvedBundleManifestRoundTripVerifiesIdentity(t *testing.T) {
	bundle, err := NewResolvedBundle(validPythonBundlePayload(), acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	content, reference, err := EncodeResolvedBundleManifest(bundle, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if reference != (providerstore.StoreObjectRef{Kind: BundleManifestStoreKind, Digest: bundle.Identity}) {
		t.Fatalf("reference = %#v", reference)
	}
	decoded, err := DecodeResolvedBundleManifest(content, reference, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Identity != bundle.Identity {
		t.Fatalf("identity = %s, want %s", decoded.Identity, bundle.Identity)
	}
}

func TestResolvedBundleManifestPublishesAndLoadsFromDeploymentStore(t *testing.T) {
	bundle, err := NewResolvedBundle(validPythonBundlePayload(), acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reference, err := PublishResolvedBundleManifest(context.Background(), store, bundle, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.ManifestPath(reference)
	if err != nil {
		t.Fatal(err)
	}
	hex := strings.TrimPrefix(string(bundle.Identity), "sha256:")
	wantPath := filepath.Join(store.Root(), "manifests", "sha256", hex[:2], hex+".json")
	if path != wantPath {
		t.Fatalf("manifest path = %q, want %q", path, wantPath)
	}
	loaded, err := LoadResolvedBundleManifest(store, reference, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Identity != bundle.Identity {
		t.Fatalf("identity = %s, want %s", loaded.Identity, bundle.Identity)
	}
}

func TestResolvedBundleManifestRejectsCorruptionAndNoncanonicalJSON(t *testing.T) {
	bundle, err := NewResolvedBundle(validPythonBundlePayload(), acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	content, reference, err := EncodeResolvedBundleManifest(bundle, acceptTestBundleOwner)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		content   []byte
		reference providerstore.StoreObjectRef
		want      string
	}{
		{
			name: "stored identity",
			content: bytes.Replace(content, []byte(bundle.Identity),
				[]byte(testDigest("f")), 1),
			reference: reference,
			want:      "does not match payload identity",
		},
		{
			name:      "reference identity",
			content:   content,
			reference: providerstore.StoreObjectRef{Kind: BundleManifestStoreKind, Digest: testDigest("e")},
			want:      "does not match store reference",
		},
		{
			name:      "reference kind",
			content:   content,
			reference: providerstore.StoreObjectRef{Kind: providerstore.BlobKind, Digest: bundle.Identity},
			want:      "reference kind",
		},
		{
			name:      "unknown field",
			content:   append([]byte(`{"unknown":true,`), content[1:]...),
			reference: reference,
			want:      "unknown field",
		},
		{
			name:      "whitespace",
			content:   append(append([]byte{}, content...), '\n'),
			reference: reference,
			want:      "not canonical JSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeResolvedBundleManifest(test.content, test.reference, acceptTestBundleOwner)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
