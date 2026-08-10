package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/controlledsession"
)

func TestControlledSessionPrivateChannelConfigV1UsesOnlyFrozenPlanAuthority(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	config, err := controlledSessionPrivateChannelConfigV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	if config.HostDirectory != plan.Channel.HostDirectory || !reflect.DeepEqual(config.Opened.Authorization, plan.Authorization) ||
		config.Opened.Columns != input.InitialColumns || config.Opened.Rows != input.InitialRows ||
		config.Opened.OutputFinalizationTimeoutMilliseconds != controlledsession.DefaultOutputFinalizationTimeoutMillisecondsV1 {
		t.Fatalf("private channel config = %#v", config)
	}
	if controlledSessionContainerExposesChannelV1(plan.Workload, plan.Channel) ||
		strings.Contains(strings.Join(plan.Workload.Environment, "\x00"), config.Opened.Authorization.Handle) ||
		strings.Contains(strings.Join(plan.Workload.Create.Args, "\x00"), plan.Channel.ContainerSocket) {
		t.Fatalf("workload plan exposes private channel authority: %#v", plan.Workload)
	}
}

func TestControlledSessionPrivateChannelConfigV1RejectsMutatedPlan(t *testing.T) {
	input, backend := controlledSessionPlanFixtureV1(t)
	plan, err := planControlledSessionV1(input, backend)
	if err != nil {
		t.Fatal(err)
	}
	plan.Authorization.Handle = "session-invalid"
	if _, err := controlledSessionPrivateChannelConfigV1(plan); err == nil || !strings.Contains(err.Error(), "64 lowercase") {
		t.Fatalf("mutated plan error = %v", err)
	}
}
