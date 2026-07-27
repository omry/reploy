package deploy

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const (
	ImageDescriptorSchemaV1 = "image-descriptor-v1"
	BaseConfigSchemaV1      = "base-config-v1"
	RootFSSubjectSchemaV1   = "rootfs-subject-v1"
)

type ImageDescriptor struct {
	Schema             string             `json:"schema"`
	Platform           blueprint.Platform `json:"platform"`
	AuthorReference    string             `json:"author_reference"`
	ImmutableReference string             `json:"immutable_reference"`
	ManifestDigest     canonical.Digest   `json:"manifest_digest"`
	ConfigDigest       canonical.Digest   `json:"config_digest"`
	RootFSDiffIDs      []canonical.Digest `json:"rootfs_diff_ids"`
}

type BaseConfig struct {
	Schema      string                      `json:"schema"`
	Environment []ConfigEnvironmentVariable `json:"environment"`
	User        string                      `json:"user"`
	WorkingDir  string                      `json:"working_dir"`
	Entrypoint  []string                    `json:"entrypoint"`
	Command     []string                    `json:"command"`
	Healthcheck string                      `json:"healthcheck"`
	StopSignal  string                      `json:"stop_signal"`
	OnBuild     []string                    `json:"on_build"`
	Volumes     []string                    `json:"volumes"`
}

type ConfigEnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (descriptor ImageDescriptor) Validate() error {
	if descriptor.Schema != ImageDescriptorSchemaV1 {
		return fmt.Errorf("image descriptor schema must be %q", ImageDescriptorSchemaV1)
	}
	if err := descriptor.Platform.Validate(); err != nil {
		return fmt.Errorf("image descriptor platform: %w", err)
	}
	if err := validateSafeImageReference("author", descriptor.AuthorReference, false); err != nil {
		return err
	}
	if err := validateSafeImageReference("immutable", descriptor.ImmutableReference, true); err != nil {
		return err
	}
	if err := descriptor.ConfigDigest.Validate(); err != nil {
		return fmt.Errorf("image config digest: %w", err)
	}
	if _, referenceDigest, repositoryDigest := strings.Cut(descriptor.ImmutableReference, "@"); repositoryDigest {
		if err := descriptor.ManifestDigest.Validate(); err != nil {
			return fmt.Errorf("registry image manifest digest: %w", err)
		}
		if string(descriptor.ManifestDigest) != referenceDigest {
			return fmt.Errorf("registry image manifest digest does not match its immutable reference")
		}
	} else {
		if descriptor.ManifestDigest != "" {
			return fmt.Errorf("local image descriptor must not fabricate a manifest digest")
		}
		if string(descriptor.ConfigDigest) != descriptor.ImmutableReference {
			return fmt.Errorf("local image immutable reference must equal its config digest")
		}
	}
	if descriptor.RootFSDiffIDs == nil || len(descriptor.RootFSDiffIDs) == 0 {
		return fmt.Errorf("image rootfs diff IDs must use a nonempty array")
	}
	for index, digest := range descriptor.RootFSDiffIDs {
		if err := digest.Validate(); err != nil {
			return fmt.Errorf("image rootfs diff ID %d: %w", index, err)
		}
	}
	return nil
}

func (config BaseConfig) Validate() error {
	if config.Schema != BaseConfigSchemaV1 {
		return fmt.Errorf("base config schema must be %q", BaseConfigSchemaV1)
	}
	if config.Environment == nil || config.Entrypoint == nil || config.Command == nil || config.OnBuild == nil || config.Volumes == nil {
		return fmt.Errorf("base config collection fields must use arrays")
	}
	for index, variable := range config.Environment {
		if !validConfigEnvironmentName(variable.Name) {
			return fmt.Errorf("base config environment name %q is invalid", variable.Name)
		}
		if index > 0 && config.Environment[index-1].Name >= variable.Name {
			return fmt.Errorf("base config environment must be unique and sorted")
		}
		if !utf8.ValidString(variable.Value) {
			return fmt.Errorf("base config environment %q is not valid UTF-8", variable.Name)
		}
	}
	for index, volume := range config.Volumes {
		if volume == "" || !path.IsAbs(volume) || path.Clean(volume) != volume || strings.Contains(volume, `\`) {
			return fmt.Errorf("base config volume %q must be a normalized absolute Linux path", volume)
		}
		if index > 0 && config.Volumes[index-1] >= volume {
			return fmt.Errorf("base config volumes must be unique and sorted")
		}
	}
	stringsToCheck := []struct {
		field string
		value string
	}{
		{field: "user", value: config.User},
		{field: "working directory", value: config.WorkingDir},
		{field: "healthcheck", value: config.Healthcheck},
		{field: "stop signal", value: config.StopSignal},
	}
	for _, item := range stringsToCheck {
		if !utf8.ValidString(item.value) {
			return fmt.Errorf("base config %s is not valid UTF-8", item.field)
		}
	}
	for _, list := range [][]string{config.Entrypoint, config.Command, config.OnBuild} {
		for _, value := range list {
			if !utf8.ValidString(value) {
				return fmt.Errorf("base config array contains invalid UTF-8")
			}
		}
	}
	return nil
}

func RootFSSubject(diffIDs []canonical.Digest) (canonical.Digest, error) {
	if diffIDs == nil || len(diffIDs) == 0 {
		return "", fmt.Errorf("rootfs subject requires a nonempty diff ID array")
	}
	for index, digest := range diffIDs {
		if err := digest.Validate(); err != nil {
			return "", fmt.Errorf("rootfs subject diff ID %d: %w", index, err)
		}
	}
	return canonical.Sum("rootfs-subject", RootFSSubjectSchemaV1, diffIDs)
}

func validateSafeImageReference(field string, reference string, immutable bool) error {
	if reference == "" || strings.TrimSpace(reference) != reference || !utf8.ValidString(reference) || strings.ContainsAny(reference, "\r\n\t") || strings.Contains(reference, "://") {
		return fmt.Errorf("image %s reference is missing or unsafe", field)
	}
	if before, after, found := strings.Cut(reference, "@"); found {
		if before == "" || strings.Contains(after, "@") {
			return fmt.Errorf("image %s reference is malformed", field)
		}
		if err := canonical.Digest(after).Validate(); err != nil {
			return fmt.Errorf("image %s reference digest: %w", field, err)
		}
		return nil
	}
	if immutable {
		if err := canonical.Digest(reference).Validate(); err != nil {
			return fmt.Errorf("image immutable reference must be a repo digest or local image ID: %w", err)
		}
	}
	return nil
}

func validConfigEnvironmentName(name string) bool {
	if name == "" || (name[0] < 'A' || name[0] > 'Z') && (name[0] < 'a' || name[0] > 'z') && name[0] != '_' {
		return false
	}
	for index := 1; index < len(name); index++ {
		char := name[index]
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}
