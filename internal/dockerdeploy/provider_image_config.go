package dockerdeploy

import (
	"fmt"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

// ProviderFinalImageConfigV1 derives the complete controlled image defaults
// used by every provider materialization layer. Base environment values remain
// available to normal runtime processes; all other supported fields use
// explicit Reploy policy instead of inheriting base execution behavior.
func ProviderFinalImageConfigV1(base deploy.BaseConfig) (providers.ImageConfigPolicy, error) {
	if err := base.Validate(); err != nil {
		return providers.ImageConfigPolicy{}, fmt.Errorf("provider final image base config: %w", err)
	}
	if len(base.OnBuild) != 0 {
		return providers.ImageConfigPolicy{}, fmt.Errorf("provider final image base config declares unsupported OnBuild instructions")
	}
	if len(base.Volumes) != 0 {
		return providers.ImageConfigPolicy{}, fmt.Errorf("provider final image base config declares unsupported volumes")
	}

	environment := make([]providers.EnvironmentVariable, 0, len(base.Environment))
	for _, variable := range base.Environment {
		environment = append(environment, providers.EnvironmentVariable{Name: variable.Name, Value: variable.Value})
	}
	policy := providers.ImageConfigPolicy{
		User:        "0:0",
		WorkingDir:  "/",
		Environment: environment,
		Entrypoint:  []string{},
		Command:     []string{},
		Healthcheck: providers.ImageHealthcheckNone,
		StopSignal:  "SIGTERM",
		Labels:      []providers.ImageLabel{},
	}
	if err := providers.ValidateImageConfigPolicy(policy); err != nil {
		return providers.ImageConfigPolicy{}, fmt.Errorf("provider final image config: %w", err)
	}
	return policy, nil
}
