package dockerdeploy

import (
	"os"
	"path/filepath"
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

func TestWriteControlledSessionNetworkPolicyV1FreezesExactRealizedPrefixes(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	if err := os.MkdirAll(plan.Channel.HostDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	realized, err := deriveControlledSessionNetworkRealizationV1([]string{"fd00:1::/64", "172.31.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeControlledSessionNetworkPolicyV1(plan, realized); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(plan.Channel.HostDirectory, controlledSessionNetworkPolicyFileNameV1)
	content, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "prefix 172.31.0.0/24\nprefix fd00:1::/64\n" +
		"controller 172.31.0.2\ncontroller fd00:1::2\n" +
		"workload 172.31.0.3\nworkload fd00:1::3\n"
	if string(content) != want || info.Mode().Perm() != 0o444 {
		t.Fatalf("network policy input = %q mode=%#o", content, info.Mode().Perm())
	}
	if err := writeControlledSessionNetworkPolicyV1(plan, realized); err == nil || !strings.Contains(err.Error(), "file exists") {
		t.Fatalf("network policy replacement error = %v", err)
	}
}

func TestWriteControlledSessionNetworkPolicyV1RejectsMissingOrInvalidRealization(t *testing.T) {
	plan := controlledSessionNetworkPlanFixtureV1(t)
	if err := os.MkdirAll(plan.Channel.HostDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, realized := range []controlledSessionNetworkRealizationV1{
		{},
		{Subnets: []string{"172.31.0.1/24"}},
		{Subnets: []string{"172.31.0.0/24", "172.31.0.0/24"}},
		{Subnets: []string{"172.31.0.0/24"}, ControllerAddresses: []string{"172.31.0.9"}, WorkloadAddresses: []string{"172.31.0.3"}},
	} {
		if err := writeControlledSessionNetworkPolicyV1(plan, realized); err == nil {
			t.Fatalf("invalid network realization %#v passed", realized)
		}
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
