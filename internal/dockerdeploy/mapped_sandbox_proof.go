package dockerdeploy

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/mappedlaunch"
)

// The trusted F effect owner supplies the immutable-image code closure and
// fresh observations of the inhibited instance. This is not operator input or
// a qualification flag. No production observation provider is supplied by E.
// The call runs inside the existing admission writer: it must not reacquire
// OperationLock. Build-lock reads and expected-plan construction precede it.
type mappedSandboxEvidenceV1 interface {
	MappedSandbox(context.Context, deploy.StateV1, mappedSandboxRequestV1) (mappedSandboxProofV1, error)
}

type mappedSandboxRequestV1 struct {
	Binding       mappedlaunch.Binding `json:"binding"`
	Container     string               `json:"container"`
	PID           string               `json:"pid"`
	RuntimePolicy canonical.Digest     `json:"runtime_policy"`
	NetworkPolicy canonical.Digest     `json:"network_policy"`
	RequiredCode  []string             `json:"required_code"`
}

type mappedCodePathV1 struct {
	Path, Resolved string
}

type mappedSandboxMountV1 struct {
	Target, Resolved string
}

type mappedSandboxProofV1 struct {
	Request       canonical.Digest
	NetworkPolicy canonical.Digest
	ReadOnlyRoot  bool
	// Code is F's complete closure from the pinned image, including loaders,
	// interpreters and dependency search paths. ObservedCode is its live view.
	Code, ObservedCode []mappedCodePathV1
	// Complete non-root mounts, including resolved symlink destinations.
	Mounts []mappedSandboxMountV1
}

func (a *ownedMappedWaiterV1) sandboxRequest() (mappedSandboxRequestV1, error) {
	p := a.prepared
	if p.ops.Operation == nil || p.ops.ValidateProfileOwner == nil || p.state.Current == nil {
		return mappedSandboxRequestV1{}, fmt.Errorf("mapped sandbox requires selected build evidence")
	}
	lock, found, err := p.ops.Operation.ReadBuildLock(p.state.Current.BuildLockDigest, p.ops.ValidateProfileOwner)
	if err != nil || !found {
		return mappedSandboxRequestV1{}, fmt.Errorf("mapped sandbox build evidence unavailable: %v", err)
	}
	policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil || policy != p.state.Current.RuntimePolicyDigest || lock.FinalImage.ConfigDigest != a.binding.Image ||
		p.state.Current.BuildLockDigest != a.binding.Generation || lock.FinalImage.Digest != p.state.Current.ImageDigest ||
		lock.FinalImage.RootFSSubject != p.state.Current.RootFSSubject {
		return mappedSandboxRequestV1{}, fmt.Errorf("mapped sandbox generation or image changed")
	}
	body, _, err := podmanApplicationCreateRequest(p.spec, p.namespace)
	separator := slices.Index(body.Cmd, "--")
	if err != nil || separator < 0 || separator+1 >= len(body.Cmd) || !mappedCodeAbsoluteV1(body.Cmd[separator+1]) {
		return mappedSandboxRequestV1{}, fmt.Errorf("mapped sandbox executable is unavailable")
	}
	required := []string{deploy.ApplicationStartupVerifierPathV1, body.Cmd[separator+1]}
	for _, protected := range lock.RuntimePolicy.ProtectedPaths {
		required = append(required, protected.Path)
	}
	slices.Sort(required)
	// ReadBuildLock already validated the closed string-only network policy.
	network, _ := canonical.Sum("mapped-network", "v1", lock.RuntimePolicy.Network)
	return mappedSandboxRequestV1{a.binding, a.container, strconv.Itoa(a.pid), policy, network, slices.Compact(required)}, nil
}

func verifyMappedSandboxV1(ctx context.Context, state deploy.StateV1, evidence deploy.IdentityMappingEvidenceV1, request mappedSandboxRequestV1) error {
	owner, ok := evidence.(mappedSandboxEvidenceV1)
	if !ok || ctx.Err() != nil {
		return fmt.Errorf("mapped sandbox observation provider is unavailable")
	}
	digest, err := canonical.Sum("mapped-sandbox", "v1", request)
	if err != nil {
		return err
	}
	copy := request
	copy.RequiredCode = slices.Clone(request.RequiredCode)
	proof, err := owner.MappedSandbox(ctx, state, copy)
	if err != nil {
		return fmt.Errorf("mapped sandbox observation failed: %w", err)
	}
	if ctx.Err() != nil || proof.Request != digest || proof.NetworkPolicy != request.NetworkPolicy || !proof.ReadOnlyRoot ||
		len(proof.Code) == 0 || !slices.Equal(proof.Code, proof.ObservedCode) || proof.Mounts == nil {
		return fmt.Errorf("mapped sandbox proof is missing, stale or substituted")
	}
	paths := make(map[string]bool, len(proof.Code))
	for index, code := range proof.Code {
		if !mappedCodeAbsoluteV1(code.Path) || !mappedCodeAbsoluteV1(code.Resolved) || index > 0 && proof.Code[index-1].Path >= code.Path {
			return fmt.Errorf("mapped code closure must contain sorted unique absolute paths")
		}
		paths[code.Path] = true
	}
	for _, required := range request.RequiredCode {
		if !paths[required] {
			return fmt.Errorf("mapped code closure omits required path %q", required)
		}
	}
	for _, mount := range proof.Mounts {
		if !mappedCodeAbsoluteV1(mount.Target) || !mappedCodeAbsoluteV1(mount.Resolved) {
			return fmt.Errorf("mapped mount observation has an invalid destination")
		}
		for _, code := range proof.Code {
			for _, destination := range []string{mount.Target, mount.Resolved} {
				if runtimePolicyPathsOverlap(destination, code.Path) || runtimePolicyPathsOverlap(destination, code.Resolved) {
					return fmt.Errorf("mapped mount shadows protected code %q", code.Path)
				}
			}
		}
	}
	return nil
}

func mappedCodeAbsoluteV1(value string) bool {
	return value != "/" && utf8.ValidString(value) && path.IsAbs(value) && path.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
