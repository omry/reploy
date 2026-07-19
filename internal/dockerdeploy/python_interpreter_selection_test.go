package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
)

func TestSelectPythonInterpreterUsesFirstCompatibleObservedRuntime(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	_, exchange := pythonResolverProbeExchange()
	interpreterResponse := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{exchange.Observations[1]}}
	commands := stubPythonInterpreterSelectionCommands(t, mustCanonicalProbeResponse(t, interpreterResponse), []string{"3.10.14\n", "3.13.2\n"}, nil)
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, testPreparedPythonResolverArtifacts(t))
	if err != nil {
		t.Fatal(err)
	}
	session.observations[exchange.Observations[0].ID] = exchange.Observations[0]
	launcher := pythonResolverSessionInput(t, session, exchange.Observations[0], providers.ExecutableRoleEnvironmentLauncher)
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "python", VersionConstraint: ">=3.11", ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	candidates := []providers.RealizedOutput{
		pythonInterpreterCandidate("lower", "/usr/bin/python3"),
		pythonInterpreterCandidate("upper", "/usr/bin/python3"),
	}
	selected, err := SelectPythonInterpreter(context.Background(), session, launcher, requirement, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Output != (providers.QualifiedOutput{Component: "upper", Name: "python"}) || selected.Facts.Value["version"] != "3.13.2" {
		t.Fatalf("selected = %#v", selected)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*commands) != 7 {
		t.Fatalf("commands = %#v", *commands)
	}
}

func TestSelectPythonInterpreterReportsUnusableExplicitCandidate(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	_, exchange := pythonResolverProbeExchange()
	interpreterResponse := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{exchange.Observations[1]}}
	commands := stubPythonInterpreterSelectionCommands(t, mustCanonicalProbeResponse(t, interpreterResponse), []string{"not-python\n"}, nil)
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, testPreparedPythonResolverArtifacts(t))
	if err != nil {
		t.Fatal(err)
	}
	session.observations[exchange.Observations[0].ID] = exchange.Observations[0]
	launcher := pythonResolverSessionInput(t, session, exchange.Observations[0], providers.ExecutableRoleEnvironmentLauncher)
	requirement := providers.ExecutableRequirement{
		ID: "interpreter", Command: "python", Supplier: "base", ValidationPolicy: providers.ValidationPolicyCompatible,
	}
	_, err = SelectPythonInterpreter(context.Background(), session, launcher, requirement, []providers.RealizedOutput{
		pythonInterpreterCandidate("base", "/usr/bin/python3"),
	})
	if err == nil || !strings.Contains(err.Error(), "not a usable Python interpreter") || !strings.Contains(err.Error(), "explicit Python interpreter executable path") {
		t.Fatalf("error = %v", err)
	}
	if closeErr := session.Close(context.Background()); closeErr != nil {
		t.Fatal(closeErr)
	}
	if len(*commands) != 5 {
		t.Fatalf("commands = %#v", *commands)
	}
}

func pythonInterpreterCandidate(component string, path string) providers.RealizedOutput {
	return providers.RealizedOutput{
		SupplierNode: providers.NodeID(component), SupplierComponent: component, Name: "python",
		Candidate: providers.ExecutableCandidate{InvocationPath: path},
	}
}

func stubPythonInterpreterSelectionCommands(t *testing.T, probeResponse []byte, inspectionResponses []string, resolveWheels func() error) *[]CommandSpec {
	t.Helper()
	previousOpen := runPythonResolverOpenCommand
	previousFollowup := runPythonResolverFollowupCommand
	commands := []CommandSpec{}
	inspectionIndex := 0
	t.Cleanup(func() {
		runPythonResolverOpenCommand = previousOpen
		runPythonResolverFollowupCommand = previousFollowup
	})
	runPythonResolverOpenCommand = func(spec CommandSpec, _ RunOptions) error {
		commands = append(commands, spec)
		return nil
	}
	runPythonResolverFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		if len(spec.Args) == 0 {
			return errors.New("empty Docker command")
		}
		if spec.Args[0] != "exec" {
			return nil
		}
		if spec.Args[len(spec.Args)-1] == ProbeContainerExecutable {
			_, _ = options.Stdout.Write(probeResponse)
			return nil
		}
		for index := 0; index+1 < len(spec.Args); index++ {
			if spec.Args[index] == "-m" && spec.Args[index+1] == "pip" {
				if resolveWheels != nil {
					return resolveWheels()
				}
				return nil
			}
		}
		if inspectionIndex >= len(inspectionResponses) {
			return errors.New("unexpected interpreter inspection")
		}
		_, _ = options.Stdout.Write([]byte(inspectionResponses[inspectionIndex]))
		inspectionIndex++
		return nil
	}
	t.Cleanup(func() {
		if inspectionIndex != len(inspectionResponses) {
			t.Errorf("inspection responses consumed = %d, want %d; commands = %#v", inspectionIndex, len(inspectionResponses), commands)
		}
	})
	return &commands
}
