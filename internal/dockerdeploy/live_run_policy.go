package dockerdeploy

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

// LiveRunConcurrencyDecisionV1 records whether one app command or shell may
// overlap another outstanding run under the resolved blueprint policy.
type LiveRunConcurrencyDecisionV1 struct {
	AllowsOverlap bool
	WritableMount string
}

func liveRunConflictErrorV1(policy blueprint.ConcurrentRunPolicy, writableMount string) error {
	return liveRunConflictMessageV1{Policy: policy, WritableMount: writableMount}
}

type liveRunConflictMessageV1 struct {
	Policy        blueprint.ConcurrentRunPolicy
	WritableMount string
}

func (conflict liveRunConflictMessageV1) Error() string {
	detail := fmt.Sprintf("allow_concurrent: %s", conflict.Policy)
	if conflict.WritableMount != "" {
		detail += fmt.Sprintf("; writable mount: %q", conflict.WritableMount)
	}
	return fmt.Sprintf(
		"another command is active or queued (%s); use --wait to queue",
		detail,
	)
}

func (liveRunConflictMessageV1) Unwrap() error {
	return deploy.ErrLiveRunConflict
}

// PlanLiveRunConcurrencyV1 applies the resolved blueprint policy to the
// effective runtime mounts. Output-file staging is private to one run, while
// output-dir is a shared writable mount.
func PlanLiveRunConcurrencyV1(
	document blueprint.Document,
	plan DockerExecutionPlan,
	output *transientOutputMount,
) (LiveRunConcurrencyDecisionV1, error) {
	switch document.Environment.AllowConcurrent {
	case blueprint.ConcurrentRunYes:
		return LiveRunConcurrencyDecisionV1{AllowsOverlap: true}, nil
	case blueprint.ConcurrentRunNo:
		return LiveRunConcurrencyDecisionV1{}, nil
	case blueprint.ConcurrentRunAuto:
	default:
		return LiveRunConcurrencyDecisionV1{}, fmt.Errorf(
			"environment.allow_concurrent must be yes, no, or auto",
		)
	}

	writable := make([]string, 0)
	for _, mount := range plan.Mounts {
		if !mount.ReadOnly {
			writable = append(writable, mount.Name)
		}
	}
	sort.Strings(writable)
	if len(writable) != 0 {
		return LiveRunConcurrencyDecisionV1{WritableMount: writable[0]}, nil
	}
	if output != nil {
		switch output.Variable {
		case runtimeOutputDirectoryVariable:
			return LiveRunConcurrencyDecisionV1{WritableMount: "--output-dir"}, nil
		case runtimeOutputFileVariable:
			// The adjacent hidden staging directory belongs only to this run.
		case "":
			return LiveRunConcurrencyDecisionV1{}, fmt.Errorf("runtime output mount variable is required")
		default:
			return LiveRunConcurrencyDecisionV1{}, fmt.Errorf("unsupported runtime output mount variable %q", output.Variable)
		}
	}
	return LiveRunConcurrencyDecisionV1{AllowsOverlap: true}, nil
}
