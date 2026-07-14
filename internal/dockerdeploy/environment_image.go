package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func resolveDockerBaseIdentity(ctx context.Context, image string, run dockerOutputRunner) (string, error) {
	if strings.TrimSpace(image) == "" {
		return "", fmt.Errorf("Docker base image is required")
	}
	if _, err := run(ctx, "pull", image); err != nil {
		return "", fmt.Errorf("pull Docker base image %s: %w", image, err)
	}
	output, err := run(ctx, "image", "inspect", "--format", "{{json .RepoDigests}}\t{{.Id}}", image)
	if err != nil {
		return "", fmt.Errorf("inspect Docker base image %s: %w", image, err)
	}
	parts := strings.SplitN(strings.TrimSpace(output), "\t", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("inspect Docker base image returned unexpected output %q", output)
	}
	var digests []string
	if err := json.Unmarshal([]byte(parts[0]), &digests); err != nil {
		return "", fmt.Errorf("decode Docker base image digests: %w", err)
	}
	if len(digests) > 0 && strings.Contains(digests[0], "@sha256:") {
		return digests[0], nil
	}
	if strings.HasPrefix(parts[1], "sha256:") {
		return parts[1], nil
	}
	return "", fmt.Errorf("Docker base image %s has no immutable digest or image ID", image)
}

var runGeneratedImagePromotionCommand = runCommand

// BuildEnvironmentImage consumes an already prepared closed wheelhouse. Bundle
// resolution and image creation happen during stage/update, never container
// startup.
func BuildEnvironmentImage(ctx context.Context, dir string, pack deploy.AppPack, state deploy.DeploymentState, options RunOptions) (deploy.DeploymentState, error) {
	if pack.Environment == nil {
		return state, fmt.Errorf("blueprint does not use the environment model")
	}
	document := *pack.Environment
	if reused, next := reuseEnvironmentImage(ctx, dir, document, state); reused {
		return next, nil
	}
	baseIdentity, err := resolveDockerBaseIdentity(ctx, document.Docker.Image, runDockerOutput)
	if err != nil {
		return state, err
	}
	selection := pythonprovider.SelectionFromBundleState(state.Bundle)
	request, err := pythonprovider.ResolveRequest(document, selection, "linux/"+runtime.GOARCH, document.Docker.Image)
	if err != nil {
		return state, err
	}
	bundleDir, err := deploymentBundleDir(dir)
	if err != nil {
		return state, err
	}
	provider := pythonprovider.ComponentProvider{Resolver: pythonprovider.PreparedBundleResolver{Dir: bundleDir, BaseIdentity: baseIdentity}}
	for _, prerequisite := range provider.Prerequisites(request) {
		if prerequisite.Source != providers.PrerequisiteBaseImage {
			continue
		}
		args := []string{"run", "--rm", "--entrypoint", prerequisite.ProbeArgv[0], baseIdentity}
		args = append(args, prerequisite.ProbeArgv[1:]...)
		output, err := runDockerOutput(ctx, args...)
		if err != nil {
			return state, fmt.Errorf("Python prerequisite %s is unavailable in %s: %w", prerequisite.Name, baseIdentity, err)
		}
		if prerequisite.Name == "python" {
			if err := pythonprovider.ValidatePythonVersion(output); err != nil {
				return state, err
			}
		}
	}
	bundle, err := provider.Resolve(ctx, request)
	if err != nil {
		return state, err
	}
	materialization, err := provider.Materialize(providers.MaterializeRequest{Bundle: bundle})
	if err != nil {
		return state, err
	}
	slot := GeneratedImageStaging
	identityPath := dir
	if state.Phase == deploy.PhaseInstalled {
		slot = GeneratedImageDeployed
		if state.Install != nil && state.Install.TargetDir != "" {
			identityPath = state.Install.TargetDir
		}
	}
	identity, err := generatedImageIdentity(document.Environment.ID, identityPath, slot, []providers.Bundle{bundle})
	if err != nil {
		return state, err
	}
	if slot == GeneratedImageDeployed {
		state, err = promotePreviousEnvironmentImage(ctx, state, identity, options)
		if err != nil {
			return state, err
		}
	}
	buildOptions := options
	buildOptions.Context = ctx
	if err := BuildGeneratedImage(GeneratedImagePlan{
		BaseImage: document.Docker.Image, BaseIdentity: baseIdentity, Tag: identity.Reference,
		BundleDir: bundleDir, Materialization: materialization, Labels: identity.Labels,
	}, buildOptions); err != nil {
		return state, err
	}
	imageID, err := runDockerOutput(ctx, "image", "inspect", "--format", "{{.Id}}", identity.Reference)
	if err != nil {
		return state, err
	}
	imageState := &deploy.GeneratedImageState{
		Reference: identity.Reference, ImageID: strings.TrimSpace(imageID), Fingerprint: identity.Fingerprint, BaseDigest: identity.BaseDigest,
	}
	if state.Images == nil {
		state.Images = &deploy.GeneratedImagesState{}
	}
	if slot == GeneratedImageStaging {
		state.Images.Staging = imageState
	} else {
		state.Images.Deployed = imageState
	}
	state.Materialization = &deploy.MaterializationState{BundleFingerprint: state.Bundle.PreparedFingerprint, Bundles: []providers.Bundle{bundle}, Executables: bundle.Executables}
	return state, nil
}

func promotePreviousEnvironmentImage(ctx context.Context, state deploy.DeploymentState, identity GeneratedImageIdentity, options RunOptions) (deploy.DeploymentState, error) {
	if state.Images == nil || state.Images.Deployed == nil {
		return state, nil
	}
	deployed := *state.Images.Deployed
	if strings.TrimSpace(deployed.Reference) == "" {
		return state, nil
	}
	if _, err := runDockerOutput(ctx, "image", "inspect", "--format", "{{.Id}}", deployed.Reference); err != nil {
		return state, nil
	}
	previousReference := identity.Repository + ":previous"
	command := CommandSpec{Name: "docker", Args: []string{"image", "tag", deployed.Reference, previousReference}}
	options.Context = ctx
	if err := runGeneratedImagePromotionCommand(command, options); err != nil {
		return state, fmt.Errorf("retain previous generated image: %w", err)
	}
	deployed.Reference = previousReference
	state.Images.Previous = &deployed
	return state, nil
}

func reuseEnvironmentImage(ctx context.Context, dir string, document blueprint.Document, state deploy.DeploymentState) (bool, deploy.DeploymentState) {
	if state.Materialization == nil || state.Images == nil || len(state.Materialization.Bundles) == 0 || state.Materialization.BundleFingerprint != state.Bundle.PreparedFingerprint {
		return false, state
	}
	slot := GeneratedImageStaging
	recorded := state.Images.Staging
	identityPath := dir
	if state.Phase == deploy.PhaseInstalled {
		slot = GeneratedImageDeployed
		recorded = state.Images.Deployed
		if state.Install != nil && state.Install.TargetDir != "" {
			identityPath = state.Install.TargetDir
		}
	}
	if recorded == nil {
		return false, state
	}
	identity, err := generatedImageIdentity(document.Environment.ID, identityPath, slot, state.Materialization.Bundles)
	if err != nil {
		return false, state
	}
	format := "{{.Id}}\t{{index .Config.Labels \"" + generatedImageOwnerLabel + "\"}}\t{{index .Config.Labels \"" + generatedImageDirectoryLabel + "\"}}\t{{index .Config.Labels \"" + generatedImageFingerprintLabel + "\"}}\t{{index .Config.Labels \"" + generatedImageBaseDigestLabel + "\"}}"
	output, err := runDockerOutput(ctx, "image", "inspect", "--format", format, identity.Reference)
	if err != nil {
		return false, state
	}
	parts := strings.Split(strings.TrimSpace(output), "\t")
	if len(parts) != 5 {
		return false, state
	}
	inspection := &GeneratedImageInspection{ImageID: parts[0], Labels: map[string]string{
		generatedImageOwnerLabel: parts[1], generatedImageDirectoryLabel: parts[2],
		generatedImageFingerprintLabel: parts[3], generatedImageBaseDigestLabel: parts[4],
	}}
	decision := generatedImageReuse(identity, recorded, inspection)
	if !decision.Reuse {
		return false, state
	}
	if slot == GeneratedImageStaging {
		state.Images.Staging = decision.Recovered
	} else {
		state.Images.Deployed = decision.Recovered
	}
	return true, state
}

func WriteResolvedRuntimeInputs(dir string, pack deploy.AppPack, state deploy.DeploymentState) ([]UpdateResult, error) {
	plan, err := ResolvedDockerExecutionPlan(dir, pack, state)
	if err != nil {
		return nil, err
	}
	document := *pack.Environment
	inputs, err := RenderDockerInputs(plan, document.Environment.ControlScript)
	if err != nil {
		return nil, err
	}
	results := []UpdateResult{}
	composePath := filepath.Join(dir, ComposeFileName)
	status, err := deploy.WriteFileIfChanged(composePath, inputs.Compose, 0o644)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: composePath, Status: status, Ownership: "runtime", Reason: "rendered resolved Docker execution plan"})
	envLines := []string{"# Private Reploy runtime inputs."}
	names := make([]string, 0, len(inputs.Environment))
	for name := range inputs.Environment {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		envLines = append(envLines, name+"="+inputs.Environment[name])
	}
	envPath := filepath.Join(dir, DockerEnvFileName)
	status, err = deploy.WriteFileIfChanged(envPath, []byte(strings.Join(envLines, "\n")+"\n"), 0o644)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: envPath, Status: status, Ownership: "runtime", Reason: "rendered resolved Docker runtime values"})
	return results, nil
}

func ResolvedDockerExecutionPlan(dir string, pack deploy.AppPack, state deploy.DeploymentState) (DockerExecutionPlan, error) {
	if pack.Environment == nil || state.Materialization == nil || state.Images == nil {
		return DockerExecutionPlan{}, fmt.Errorf("resolved environment materialization is unavailable")
	}
	document := *pack.Environment
	phase := blueprint.PhaseStaged
	image := state.Images.Staging
	var scope *blueprint.InstallScope
	installTarget := ""
	if state.Phase == deploy.PhaseInstalled {
		phase = blueprint.PhaseInstalled
		image = state.Images.Deployed
		if state.Install == nil {
			return DockerExecutionPlan{}, fmt.Errorf("installed state is missing install metadata")
		}
		value := blueprint.InstallScope(state.Install.Scope)
		scope = &value
		installTarget = state.Install.TargetDir
	}
	if image == nil {
		return DockerExecutionPlan{}, fmt.Errorf("generated image state is unavailable for %s", phase)
	}
	host := blueprint.HostLinux
	switch runtime.GOOS {
	case "darwin":
		host = blueprint.HostMacOS
	case "windows":
		host = blueprint.HostWindows
	}
	context := DockerPlanContext{
		DeploymentDir: dir, InstallTarget: installTarget, Phase: phase, Scope: scope,
		GeneratedImage: image.Reference, Host: host, UID: os.Getuid(), GID: os.Getgid(),
	}
	if scope != nil && *scope == blueprint.InstallScopeSystem {
		runAs := document.Environment.Install.System.RunAs
		uid, gid, err := parseInstallOwner(runAs.User + ":" + runAs.Group)
		if err != nil {
			return DockerExecutionPlan{}, err
		}
		context.SystemUser, context.SystemGroup, context.UID, context.GID = runAs.User, runAs.Group, uid, gid
	}
	if state.Install != nil {
		context.PortOverrides = map[string]int{}
		for name, binding := range state.Install.Ports {
			port, err := strconv.Atoi(binding.HostPort)
			if err != nil {
				return DockerExecutionPlan{}, err
			}
			context.PortOverrides[name] = port
		}
	}
	plan, err := PlanDockerExecution(document, context)
	if err != nil {
		return DockerExecutionPlan{}, err
	}
	if plan.Workload != nil {
		command, err := ResolveEnvironmentCommandForPlan(document, state.Materialization.Executables, plan, plan.Workload.Command, nil)
		if err != nil {
			return DockerExecutionPlan{}, err
		}
		plan.Workload.Argv = command.Argv
	}
	return plan, nil
}
