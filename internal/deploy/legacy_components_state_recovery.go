package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

type legacyComponentsRequestOverlayV1 struct {
	Schema          string                                 `json:"schema"`
	SelectedOptions []legacyComponentsQualifiedOptionV1    `json:"selected_options"`
	DirectPackages  []legacyComponentsDirectPackageRequest `json:"direct_packages"`
}

type legacyComponentsQualifiedOptionV1 struct {
	Component string `json:"component"`
	Option    string `json:"option"`
}

type legacyComponentsDirectPackageRequest struct {
	Component string                            `json:"component"`
	Package   providers.CanonicalPackageRequest `json:"package"`
}

type legacyComponentsStagingStateV1 struct {
	Schema          string                           `json:"schema"`
	Blueprint       blueprint.ResolvedDocumentV1     `json:"blueprint"`
	BlueprintSource string                           `json:"blueprint_source"`
	Platform        blueprint.Platform               `json:"platform"`
	Overlay         legacyComponentsRequestOverlayV1 `json:"overlay"`
	Current         *EnvironmentGenerationState      `json:"current"`
	Staging         *StagingStateV1                  `json:"staging"`
	Deployment      json.RawMessage                  `json:"deployment"`
}

// RecoverLegacyComponentsStagingStateV1 atomically replaces only the
// recognized unreleased components-based staging shape. The operation retains
// the current generation and converts its request overlay without touching the
// package-override sidecar or managed application data.
func (lock *OperationLock) RecoverLegacyComponentsStagingStateV1(
	selectPlatform DesiredPlatformSelector,
	validatePackage PackageRequestValidator,
) (DesiredStateUpdateResult, error) {
	if selectPlatform == nil {
		return DesiredStateUpdateResult{}, fmt.Errorf(
			"recover legacy staging state requires a platform selector",
		)
	}
	path, original, err := lock.readRawStateForRecoveryV1()
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	legacy, err := decodeLegacyComponentsStagingStateV1(original)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	recovery, err := blueprint.RecoverLegacyComponentsBlueprintV1(
		legacy.BlueprintSource,
		legacy.Blueprint,
	)
	if err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf(
			"recover legacy staging blueprint: %w",
			err,
		)
	}
	overlay, err := convertLegacyComponentsRequestOverlayV1(
		legacy.Overlay,
		recovery.ComponentTypes,
	)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	overlay, err = NormalizeRequestOverlayV1(
		recovery.Document,
		overlay,
		validatePackage,
	)
	if err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf(
			"retain legacy staging request overlay: %w",
			err,
		)
	}
	selected, err := selectPlatform(recovery.Document)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	payload, err := blueprint.EncodeResolvedDocumentV1(recovery.Document)
	if err != nil {
		return DesiredStateUpdateResult{}, err
	}
	candidate := StateV1{
		Schema:          StateSchemaV1,
		Blueprint:       payload,
		BlueprintSource: recovery.Source,
		Platform:        selected,
		Overlay:         overlay,
		Current:         legacy.Current,
		Staging:         legacy.Staging,
	}
	if err := ValidateStateV1(candidate); err != nil {
		return DesiredStateUpdateResult{}, fmt.Errorf(
			"validate recovered staging state: %w",
			err,
		)
	}
	if err := lock.commitRecoveredStateV1(path, original, candidate); err != nil {
		return DesiredStateUpdateResult{}, err
	}
	return DesiredStateUpdateResult{State: candidate, Changed: true}, nil
}

func decodeLegacyComponentsStagingStateV1(content []byte) (legacyComponentsStagingStateV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var state legacyComponentsStagingStateV1
	if err := decoder.Decode(&state); err != nil {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"decode legacy staging state: %w",
			err,
		)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return legacyComponentsStagingStateV1{}, fmt.Errorf(
				"legacy staging state contains trailing JSON",
			)
		}
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"decode legacy staging state trailer: %w",
			err,
		)
	}
	if state.Schema != StateSchemaV1 {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"legacy staging state schema must be %q",
			StateSchemaV1,
		)
	}
	if !blueprint.HasLegacyComponentsResolvedShapeV1(state.Blueprint) {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"staging state is not the recognized legacy components shape",
		)
	}
	if state.BlueprintSource == "" {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"legacy staging state has no retained blueprint source",
		)
	}
	if state.Staging == nil || state.Staging.Schema != StagingStateSchemaV1 {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"forced legacy recovery requires a staging deployment",
		)
	}
	if len(state.Deployment) != 0 && string(state.Deployment) != "null" {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"forced legacy recovery does not replace an installed deployment",
		)
	}
	if err := state.Platform.Validate(); err != nil {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"legacy staging platform: %w",
			err,
		)
	}
	if state.Current != nil {
		if err := ValidateEnvironmentGenerationState(*state.Current); err != nil {
			return legacyComponentsStagingStateV1{}, fmt.Errorf(
				"legacy staging current generation: %w",
				err,
			)
		}
	}
	if state.Overlay.Schema != RequestOverlaySchemaV1 {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"legacy staging request overlay schema must be %q",
			RequestOverlaySchemaV1,
		)
	}
	if state.Overlay.SelectedOptions == nil || state.Overlay.DirectPackages == nil {
		return legacyComponentsStagingStateV1{}, fmt.Errorf(
			"legacy staging request overlay must use arrays",
		)
	}
	return state, nil
}

func convertLegacyComponentsRequestOverlayV1(
	legacy legacyComponentsRequestOverlayV1,
	componentTypes map[string]blueprint.ComponentType,
) (RequestOverlayV1, error) {
	overlay := EmptyRequestOverlayV1()
	for _, option := range legacy.SelectedOptions {
		componentType, found := componentTypes[option.Component]
		if !found || componentType != blueprint.ComponentTypePython {
			return RequestOverlayV1{}, fmt.Errorf(
				"legacy staging request overlay option targets unsupported component %q",
				option.Component,
			)
		}
		overlay.SelectedOptions = append(overlay.SelectedOptions, QualifiedOption{
			Application: option.Component,
			Option:      option.Option,
		})
	}
	for _, request := range legacy.DirectPackages {
		componentType, found := componentTypes[request.Component]
		if !found || componentType != blueprint.ComponentTypePython {
			return RequestOverlayV1{}, fmt.Errorf(
				"legacy staging direct package targets unsupported component %q",
				request.Component,
			)
		}
		overlay.DirectPackages = append(overlay.DirectPackages, DirectPackageRequest{
			Contribution: blueprint.ApplicationContributionID(
				request.Component,
				blueprint.ContributionProviderPython,
			),
			Package: request.Package,
		})
	}
	return overlay, nil
}

func (lock *OperationLock) readRawStateForRecoveryV1() (string, []byte, error) {
	if lock == nil {
		return "", nil, fmt.Errorf("read recovery state requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	path, err := lock.statePathV1Locked()
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, fmt.Errorf("staging state is missing; run `reploy stage` first")
	}
	if err != nil {
		return "", nil, fmt.Errorf("inspect recovery state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("state path must be a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read recovery state: %w", err)
	}
	return path, content, nil
}

func (lock *OperationLock) commitRecoveredStateV1(
	path string,
	expected []byte,
	state StateV1,
) error {
	if lock == nil {
		return fmt.Errorf("commit recovered state requires an operation lock")
	}
	lock.mutex.Lock()
	defer lock.mutex.Unlock()
	currentPath, err := lock.statePathV1Locked()
	if err != nil {
		return err
	}
	if currentPath != path {
		return fmt.Errorf("recovery state path changed before commit")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reread recovery state: %w", err)
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("staging state changed before forced recovery")
	}
	content, err := EncodeStateV1(state)
	if err != nil {
		return err
	}
	if err := writeAtomicStateFile(path, content, 0o600); err != nil {
		return fmt.Errorf("commit recovered state: %w", err)
	}
	return nil
}
