package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

const BundleManifestStoreKind = providerstore.BundleManifestKind

func PublishResolvedBundleManifest(ctx context.Context, store providerstore.Store, bundle ResolvedBundle, validateOwner ResolvedBundleOwnerValidator) (providerstore.StoreObjectRef, error) {
	content, reference, err := EncodeResolvedBundleManifest(bundle, validateOwner)
	if err != nil {
		return providerstore.StoreObjectRef{}, err
	}
	if err := store.PublishManifest(ctx, reference, content); err != nil {
		return providerstore.StoreObjectRef{}, err
	}
	return reference, nil
}

func LoadResolvedBundleManifest(store providerstore.Store, reference providerstore.StoreObjectRef, validateOwner ResolvedBundleOwnerValidator) (ResolvedBundle, error) {
	content, err := store.LoadManifest(reference)
	if err != nil {
		return ResolvedBundle{}, err
	}
	return DecodeResolvedBundleManifest(content, reference, validateOwner)
}

func EncodeResolvedBundleManifest(bundle ResolvedBundle, validateOwner ResolvedBundleOwnerValidator) ([]byte, providerstore.StoreObjectRef, error) {
	if err := ValidateResolvedBundle(bundle, validateOwner); err != nil {
		return nil, providerstore.StoreObjectRef{}, fmt.Errorf("encode resolved bundle manifest: %w", err)
	}
	content, err := canonical.Marshal(bundle)
	if err != nil {
		return nil, providerstore.StoreObjectRef{}, fmt.Errorf("encode resolved bundle manifest: %w", err)
	}
	reference := providerstore.StoreObjectRef{Kind: BundleManifestStoreKind, Digest: bundle.Identity}
	if err := reference.Validate(); err != nil {
		return nil, providerstore.StoreObjectRef{}, err
	}
	return content, reference, nil
}

func DecodeResolvedBundleManifest(content []byte, reference providerstore.StoreObjectRef, validateOwner ResolvedBundleOwnerValidator) (ResolvedBundle, error) {
	if err := reference.Validate(); err != nil {
		return ResolvedBundle{}, fmt.Errorf("resolved bundle manifest reference: %w", err)
	}
	if reference.Kind != BundleManifestStoreKind {
		return ResolvedBundle{}, fmt.Errorf("resolved bundle manifest reference kind must be %q", BundleManifestStoreKind)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var bundle ResolvedBundle
	if err := decoder.Decode(&bundle); err != nil {
		return ResolvedBundle{}, fmt.Errorf("decode resolved bundle manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ResolvedBundle{}, fmt.Errorf("resolved bundle manifest contains trailing JSON")
		}
		return ResolvedBundle{}, fmt.Errorf("decode resolved bundle manifest trailer: %w", err)
	}
	if err := ValidateResolvedBundle(bundle, validateOwner); err != nil {
		return ResolvedBundle{}, fmt.Errorf("validate resolved bundle manifest: %w", err)
	}
	if bundle.Identity != reference.Digest {
		return ResolvedBundle{}, fmt.Errorf("resolved bundle manifest identity %s does not match store reference %s", bundle.Identity, reference.Digest)
	}
	canonicalContent, err := canonical.Marshal(bundle)
	if err != nil {
		return ResolvedBundle{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return ResolvedBundle{}, fmt.Errorf("resolved bundle manifest is not canonical JSON")
	}
	return bundle, nil
}
