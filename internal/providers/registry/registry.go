package registry

import (
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/apt"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func Plan(input providers.PlanInput) (providers.ProviderPlanV1, error) {
	return providers.BuildProviderPlanV1(input, apt.ComponentProvider{}, pythonprovider.ComponentProvider{})
}
