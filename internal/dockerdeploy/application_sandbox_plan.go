package dockerdeploy

import (
	"fmt"
	"path"
	"strconv"
)

// ApplicationSandboxPlanV1 is the common security boundary consumed by every
// application-container renderer. It contains only policies that Reploy
// currently enforces; later sandbox slices extend this plan rather than adding
// renderer-specific flags.
type ApplicationSandboxPlanV1 struct {
	RuntimeUser   RuntimeUserPlan
	ReadOnlyRoot  bool
	TemporaryHome string
}

func newApplicationSandboxPlanV1(runtimeUser RuntimeUserPlan) ApplicationSandboxPlanV1 {
	return ApplicationSandboxPlanV1{
		RuntimeUser:   runtimeUser,
		ReadOnlyRoot:  true,
		TemporaryHome: environmentTemporaryHome,
	}
}

func ValidateApplicationSandboxPlanV1(plan ApplicationSandboxPlanV1) error {
	if plan.RuntimeUser.UID < 0 || plan.RuntimeUser.GID < 0 {
		return fmt.Errorf("application sandbox requires a non-negative numeric UID and GID")
	}
	wantUser := strconv.Itoa(plan.RuntimeUser.UID) + ":" + strconv.Itoa(plan.RuntimeUser.GID)
	if plan.RuntimeUser.DockerUser != wantUser {
		return fmt.Errorf("application sandbox Docker user must match its numeric UID and GID")
	}
	if !plan.ReadOnlyRoot {
		return fmt.Errorf("application sandbox requires a read-only container root")
	}
	if plan.TemporaryHome != environmentTemporaryHome || !path.IsAbs(plan.TemporaryHome) || path.Clean(plan.TemporaryHome) != plan.TemporaryHome {
		return fmt.Errorf("application sandbox temporary home must be %s", environmentTemporaryHome)
	}
	return nil
}
