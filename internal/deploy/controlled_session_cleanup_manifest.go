package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/omry/reploy/internal/canonical"
)

// ControlledSessionCleanupManifest is the complete immutable resource
// selection a session watchdog may receive. It is derived from durable
// ownership rather than accepting later resource choices.
//
// Networks and volumes are explicit arrays even while controlled sessions do
// not create either resource. Future slices may populate them only after their
// exact identities become part of durable ownership.
type ControlledSessionCleanupManifest struct {
	LiveRunID        string                                `json:"live_run_id"`
	BootSession      string                                `json:"boot_session"`
	ChannelDirectory string                                `json:"channel_directory"`
	Controller       ControlledSessionContainerOwnershipV1 `json:"controller"`
	Workload         ControlledSessionContainerOwnershipV1 `json:"workload"`
	Networks         []string                              `json:"networks"`
	Volumes          []string                              `json:"volumes"`
}

// ControlledSessionCleanupManifestFromOwnership creates the watchdog input
// from the exact durable ownership record. The session handle is deliberately
// omitted because cleanup does not need protocol authority.
func ControlledSessionCleanupManifestFromOwnership(ownership ControlledSessionOwnershipV1) (ControlledSessionCleanupManifest, error) {
	if err := validateControlledSessionOwnershipV1(ownership); err != nil {
		return ControlledSessionCleanupManifest{}, fmt.Errorf("controlled-session cleanup manifest ownership: %w", err)
	}
	manifest := ControlledSessionCleanupManifest{
		LiveRunID: ownership.LiveRunID, BootSession: ownership.BootSession,
		ChannelDirectory: ownership.ChannelDirectory,
		Controller:       ownership.Controller, Workload: ownership.Workload,
		Networks: []string{}, Volumes: []string{},
	}
	if err := ValidateControlledSessionCleanupManifest(manifest); err != nil {
		return ControlledSessionCleanupManifest{}, err
	}
	return manifest, nil
}

func ValidateControlledSessionCleanupManifest(manifest ControlledSessionCleanupManifest) error {
	if err := ValidateLiveRunIDV1(manifest.LiveRunID); err != nil {
		return fmt.Errorf("controlled-session cleanup manifest live run ID: %w", err)
	}
	if err := validateBootSessionIDV1(manifest.BootSession); err != nil {
		return fmt.Errorf("controlled-session cleanup manifest: %w", err)
	}
	if !filepath.IsAbs(manifest.ChannelDirectory) || filepath.Clean(manifest.ChannelDirectory) != manifest.ChannelDirectory || !safeRecoveryIdentity(manifest.ChannelDirectory) {
		return fmt.Errorf("controlled-session cleanup manifest channel directory must be a clean absolute path")
	}
	sessionsDirectory := filepath.Dir(manifest.ChannelDirectory)
	if filepath.Base(manifest.ChannelDirectory) != manifest.LiveRunID || filepath.Base(sessionsDirectory) != "sessions" ||
		filepath.Base(filepath.Dir(sessionsDirectory)) != ".reploy" {
		return fmt.Errorf("controlled-session cleanup manifest channel directory must identify the live-run private session directory")
	}
	if err := validateControlledSessionContainerOwnershipV1(manifest.Controller, "controller"); err != nil {
		return fmt.Errorf("controlled-session cleanup manifest controller: %w", err)
	}
	if err := validateControlledSessionContainerOwnershipV1(manifest.Workload, "workload"); err != nil {
		return fmt.Errorf("controlled-session cleanup manifest workload: %w", err)
	}
	if manifest.Controller.ID == manifest.Workload.ID {
		return fmt.Errorf("controlled-session cleanup manifest containers must be different")
	}
	if manifest.Networks == nil || manifest.Volumes == nil {
		return fmt.Errorf("controlled-session cleanup manifest networks and volumes must use arrays")
	}
	if err := validateControlledSessionCleanupResourceIDs(manifest.Networks, "network"); err != nil {
		return err
	}
	if err := validateControlledSessionCleanupResourceIDs(manifest.Volumes, "volume"); err != nil {
		return err
	}
	return nil
}

func validateControlledSessionCleanupResourceIDs(resources []string, kind string) error {
	for index, resource := range resources {
		if !safeRecoveryIdentity(resource) {
			return fmt.Errorf("controlled-session cleanup manifest %s identity must be nonempty safe text", kind)
		}
		if index > 0 && resources[index-1] >= resource {
			return fmt.Errorf("controlled-session cleanup manifest %s identities must be sorted and unique", kind)
		}
	}
	return nil
}

func EncodeControlledSessionCleanupManifest(manifest ControlledSessionCleanupManifest) ([]byte, error) {
	if err := ValidateControlledSessionCleanupManifest(manifest); err != nil {
		return nil, err
	}
	content, err := canonical.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode controlled-session cleanup manifest: %w", err)
	}
	return content, nil
}

func DecodeControlledSessionCleanupManifest(content []byte) (ControlledSessionCleanupManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var manifest ControlledSessionCleanupManifest
	if err := decoder.Decode(&manifest); err != nil {
		return ControlledSessionCleanupManifest{}, fmt.Errorf("decode controlled-session cleanup manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ControlledSessionCleanupManifest{}, fmt.Errorf("controlled-session cleanup manifest contains trailing JSON")
		}
		return ControlledSessionCleanupManifest{}, fmt.Errorf("decode controlled-session cleanup manifest trailer: %w", err)
	}
	if err := ValidateControlledSessionCleanupManifest(manifest); err != nil {
		return ControlledSessionCleanupManifest{}, err
	}
	canonicalContent, err := canonical.Marshal(manifest)
	if err != nil {
		return ControlledSessionCleanupManifest{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return ControlledSessionCleanupManifest{}, fmt.Errorf("controlled-session cleanup manifest is not canonical JSON")
	}
	return manifest, nil
}
