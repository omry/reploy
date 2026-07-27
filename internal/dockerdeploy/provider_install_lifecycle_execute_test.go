package dockerdeploy

import (
	"context"
	"reflect"
	"testing"
)

func TestExecuteProviderInstallAfterInstallV1RunsPreplannedOperations(t *testing.T) {
	events := []string{}
	locked := lockedProviderInstallV1{Plan: providerInstallationPlanV1{AfterInstall: LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleCommand, Event: "after_install", Command: &ResolvedEnvironmentCommand{Name: "prepare", Argv: []string{"/opt/prepare"}}},
	}}}}
	if err := executeProviderInstallAfterInstallWithV1(t.Context(), locked, providerInstallLifecycleExecutionBackendV1{
		executor: func(lockedProviderInstallV1) LifecycleExecutor {
			return LifecycleExecutor{RunCommand: func(_ context.Context, command ResolvedEnvironmentCommand) error {
				events = append(events, command.Name)
				return nil
			}}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"prepare"}) {
		t.Fatalf("after_install events = %v", events)
	}
}

func TestExecuteProviderInstallStartV1KeepsLifecycleOrder(t *testing.T) {
	events := []string{}
	endpoint := EndpointExecutionPlan{Scheme: "http", ContainerPort: 8080}
	locked := lockedProviderInstallV1{Plan: providerInstallationPlanV1{Start: LifecyclePlan{Operations: []LifecycleOperation{
		{Kind: LifecycleCommand, Event: "before_start", Command: &ResolvedEnvironmentCommand{Name: "before", Argv: []string{"/opt/before"}}},
		{Kind: LifecycleStart, Event: "start"},
		{Kind: LifecycleReadiness, Event: "after_start", Endpoint: &endpoint},
		{Kind: LifecycleCommand, Event: "after_start", Command: &ResolvedEnvironmentCommand{Name: "after", Argv: []string{"/opt/after"}}},
	}}}}
	if err := executeProviderInstallStartWithV1(t.Context(), locked, providerInstallLifecycleExecutionBackendV1{
		executor: func(lockedProviderInstallV1) LifecycleExecutor {
			return LifecycleExecutor{
				RunCommand: func(_ context.Context, command ResolvedEnvironmentCommand) error {
					events = append(events, command.Name)
					return nil
				},
				Readiness: func(context.Context, EndpointExecutionPlan) error {
					events = append(events, "readiness")
					return nil
				},
			}
		},
		start: func(context.Context, lockedProviderInstallV1) error {
			events = append(events, "start")
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"before", "start", "readiness", "after"}) {
		t.Fatalf("start events = %v", events)
	}
}
