package dockerdeploy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type GeneratedImageInspection struct {
	ImageID string
	Labels  map[string]string
}

type GeneratedImageReuse struct {
	Reuse     bool
	Recovered *deploy.GeneratedImageState
	Reason    string
}

// generatedImageReuse requires both semantic identity and Docker labels. State
// can be recovered from an owned image, but stale state alone is never trusted.
func generatedImageReuse(identity GeneratedImageIdentity, recorded *deploy.GeneratedImageState, inspected *GeneratedImageInspection) GeneratedImageReuse {
	if inspected == nil {
		return GeneratedImageReuse{Reason: "generated image is missing"}
	}
	if inspected.Labels[generatedImageOwnerLabel] != "reploy" ||
		inspected.Labels[generatedImageDirectoryLabel] != identity.DirectoryID ||
		inspected.Labels[generatedImageFingerprintLabel] != identity.Fingerprint ||
		inspected.Labels[generatedImageBaseDigestLabel] != identity.BaseDigest {
		return GeneratedImageReuse{Reason: "generated image labels do not match resolved inputs"}
	}
	recovered := &deploy.GeneratedImageState{
		Reference: identity.Reference, ImageID: inspected.ImageID,
		Fingerprint: identity.Fingerprint, BaseDigest: identity.BaseDigest,
	}
	if recorded != nil {
		if recorded.Reference != identity.Reference || recorded.Fingerprint != identity.Fingerprint || recorded.ImageID != "" && recorded.ImageID != inspected.ImageID {
			return GeneratedImageReuse{Reuse: true, Recovered: recovered, Reason: "recovered matching generated image from Docker labels"}
		}
		return GeneratedImageReuse{Reuse: true, Recovered: recorded, Reason: "recorded generated image matches Docker"}
	}
	return GeneratedImageReuse{Reuse: true, Recovered: recovered, Reason: "recovered matching generated image from Docker labels"}
}

func promoteGeneratedImageState(current *deploy.GeneratedImagesState, staging deploy.GeneratedImageState, deployedReference string, previousReference string) *deploy.GeneratedImagesState {
	next := &deploy.GeneratedImagesState{}
	if current != nil && current.Deployed != nil {
		previous := *current.Deployed
		previous.Reference = previousReference
		next.Previous = &previous
	}
	deployed := staging
	deployed.Reference = deployedReference
	next.Deployed = &deployed
	return next
}

func generatedImagePromotionCommands(repository string, hasDeployed bool) []CommandSpec {
	commands := []CommandSpec{}
	if hasDeployed {
		commands = append(commands, CommandSpec{Name: "docker", Args: []string{"image", "tag", repository + ":deployed", repository + ":previous"}})
	}
	commands = append(commands, CommandSpec{Name: "docker", Args: []string{"image", "tag", repository + ":staging", repository + ":deployed"}})
	return commands
}

func generatedImageCleanupListCommand(identity GeneratedImageIdentity) CommandSpec {
	return CommandSpec{Name: "docker", Args: []string{
		"image", "ls",
		"--filter", "label=" + generatedImageOwnerLabel + "=reploy",
		"--filter", "label=" + generatedImageDirectoryLabel + "=" + identity.DirectoryID,
		"--format", "{{.Repository}}:{{.Tag}}",
	}}
}

// generatedImageCleanupCommand removes only the directory-keyed references
// Reploy owns. Docker remains responsible for shared layers and build-cache GC.
func generatedImageCleanupCommand(identity GeneratedImageIdentity, discovered []string) (CommandSpec, error) {
	allowed := map[string]bool{
		identity.Repository + ":staging":  true,
		identity.Repository + ":deployed": true,
		identity.Repository + ":previous": true,
	}
	unique := map[string]bool{}
	for _, reference := range discovered {
		reference = strings.TrimSpace(reference)
		if reference == "" || reference == "<none>:<none>" {
			continue
		}
		if !allowed[reference] {
			return CommandSpec{}, fmt.Errorf("refusing to remove generated image outside directory identity: %s", reference)
		}
		unique[reference] = true
	}
	references := make([]string, 0, len(unique))
	for reference := range unique {
		references = append(references, reference)
	}
	sort.Strings(references)
	if len(references) == 0 {
		return CommandSpec{}, nil
	}
	return CommandSpec{Name: "docker", Args: append([]string{"image", "rm"}, references...)}, nil
}
