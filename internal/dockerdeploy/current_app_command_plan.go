package dockerdeploy

import "github.com/omry/reploy/internal/providers"

// PlanCurrentAppCommandV1 matches a public command trigger and resolves its
// executable exclusively from the current build's locked output catalog.
func PlanCurrentAppCommandV1(
	plan CurrentRuntimePlanV1,
	catalog []providers.RealizedOutput,
	arguments []string,
	deployedOnly bool,
) (ResolvedEnvironmentCommand, error) {
	name, forwarded, err := MatchEnvironmentCommand(plan.Document, arguments, deployedOnly)
	if err != nil {
		return ResolvedEnvironmentCommand{}, err
	}
	return resolveLockedEnvironmentCommandForPlanV1(plan.Document, catalog, plan.Docker, name, forwarded)
}
