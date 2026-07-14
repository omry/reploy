package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestGeneratedImageReuseAndRecovery(t *testing.T) {
	identity := GeneratedImageIdentity{
		DirectoryID: "abcd1234", Repository: "reploy/demo-abcd1234", Reference: "reploy/demo-abcd1234:staging",
		Fingerprint: strings.Repeat("a", 64), BaseDigest: "python@sha256:base",
	}
	inspection := &GeneratedImageInspection{ImageID: "sha256:image", Labels: map[string]string{
		generatedImageOwnerLabel: "reploy", generatedImageDirectoryLabel: "abcd1234",
		generatedImageFingerprintLabel: identity.Fingerprint, generatedImageBaseDigestLabel: identity.BaseDigest,
	}}
	decision := generatedImageReuse(identity, nil, inspection)
	if !decision.Reuse || decision.Recovered == nil || decision.Recovered.ImageID != "sha256:image" {
		t.Fatalf("decision = %#v", decision)
	}
	inspection.Labels[generatedImageFingerprintLabel] = strings.Repeat("b", 64)
	if decision := generatedImageReuse(identity, nil, inspection); decision.Reuse {
		t.Fatalf("stale image was reused: %#v", decision)
	}
}

func TestPromotePreviousEnvironmentImageTagsRecordedDeployment(t *testing.T) {
	previousOutput := runDockerOutput
	previousCommand := runGeneratedImagePromotionCommand
	t.Cleanup(func() {
		runDockerOutput = previousOutput
		runGeneratedImagePromotionCommand = previousCommand
	})
	runDockerOutput = func(context.Context, ...string) (string, error) { return "sha256:old", nil }
	var command CommandSpec
	runGeneratedImagePromotionCommand = func(spec CommandSpec, _ RunOptions) error {
		command = spec
		return nil
	}
	state := deploy.DeploymentState{Images: &deploy.GeneratedImagesState{Deployed: &deploy.GeneratedImageState{
		Reference: "reploy/demo-abcd:deployed", ImageID: "sha256:old", Fingerprint: "old", BaseDigest: "base",
	}}}
	next, err := promotePreviousEnvironmentImage(context.Background(), state, GeneratedImageIdentity{Repository: "reploy/demo-abcd"}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(command.Args, " "); got != "image tag reploy/demo-abcd:deployed reploy/demo-abcd:previous" {
		t.Fatalf("promotion command = %q", got)
	}
	if next.Images.Previous == nil || next.Images.Previous.Reference != "reploy/demo-abcd:previous" || next.Images.Previous.ImageID != "sha256:old" {
		t.Fatalf("next image state = %#v", next.Images)
	}
}

func TestPromoteGeneratedImageStateRetainsPrevious(t *testing.T) {
	current := &deploy.GeneratedImagesState{Deployed: &deploy.GeneratedImageState{Reference: "repo:deployed", ImageID: "old", Fingerprint: "old"}}
	staging := deploy.GeneratedImageState{Reference: "repo:staging", ImageID: "new", Fingerprint: "new"}
	next := promoteGeneratedImageState(current, staging, "repo:deployed", "repo:previous")
	if next.Deployed.ImageID != "new" || next.Deployed.Reference != "repo:deployed" || next.Previous.ImageID != "old" || next.Previous.Reference != "repo:previous" {
		t.Fatalf("next state = %#v", next)
	}
	commands := generatedImagePromotionCommands("repo", true)
	if got := []string{strings.Join(commands[0].Args, " "), strings.Join(commands[1].Args, " ")}; !reflect.DeepEqual(got, []string{
		"image tag repo:deployed repo:previous", "image tag repo:staging repo:deployed",
	}) {
		t.Fatalf("commands = %#v", got)
	}
}

func TestGeneratedImageCleanupIsDirectoryScoped(t *testing.T) {
	identity := GeneratedImageIdentity{DirectoryID: "abcd1234", Repository: "reploy/demo-abcd1234"}
	list := generatedImageCleanupListCommand(identity)
	if joined := strings.Join(list.Args, " "); !strings.Contains(joined, "label=io.reploy.directory=abcd1234") {
		t.Fatalf("list command = %q", joined)
	}
	remove, err := generatedImageCleanupCommand(identity, []string{"reploy/demo-abcd1234:staging", "reploy/demo-abcd1234:deployed"})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(remove.Args, " "); strings.Contains(joined, "prune") || !strings.Contains(joined, "reploy/demo-abcd1234:staging") {
		t.Fatalf("remove command = %q", joined)
	}
	if _, err := generatedImageCleanupCommand(identity, []string{"someone/else:latest"}); err == nil {
		t.Fatal("expected unowned reference rejection")
	}
}
