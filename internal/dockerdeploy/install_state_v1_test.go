package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestInstallationStateForPlanV1CapturesAndSortsLocalFacts(t *testing.T) {
	plan := installPlan{
		TargetDir: "/opt/demo", Scope: InstallScopeSystem, Service: "demo",
		UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
		ContainerName: "demo", NetworkName: "demo",
		Ports: []dockerPortBinding{
			{Name: "http", HostBind: "127.0.0.1", HostPort: "19000", ContainerPort: "9000"},
			{Name: "admin", HostBind: "127.0.0.1", HostPort: "19001", ContainerPort: "9001"},
		},
	}

	state, err := installationStateForPlanV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: "/opt/demo", Scope: "system", Service: "demo",
		UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
		ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{
			{Name: "admin", HostBind: "127.0.0.1", HostPort: "19001", ContainerPort: "9001"},
			{Name: "http", HostBind: "127.0.0.1", HostPort: "19000", ContainerPort: "9000"},
		},
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("installation state=%#v want=%#v", state, want)
	}
	if plan.Ports[0].Name != "http" {
		t.Fatal("conversion reordered the install plan")
	}
}

func TestInstallationStateForPlanV1RejectsInvalidPlanFacts(t *testing.T) {
	_, err := installationStateForPlanV1(installPlan{
		TargetDir: "/opt/demo", Scope: InstallScopeSystem, Service: "demo",
		InstanceID: "demo-1", ComposeProject: "demo-1", ContainerName: "demo", NetworkName: "demo",
		Ports: []dockerPortBinding{
			{Name: "http", HostBind: "127.0.0.1", HostPort: "19000", ContainerPort: "9000"},
			{Name: "http", HostBind: "127.0.0.1", HostPort: "19001", ContainerPort: "9001"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("duplicate plan port error=%v", err)
	}
}
