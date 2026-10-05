package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/toolcatalog"
)

// ApplicationPortablePythonGraphResultV1 retains the exact binding acquisition
// handoff for the subsequent runtime-payload and lock transaction. It does not
// publish an application image or make an ordinary runtime request consumable.
type ApplicationPortablePythonGraphResultV1 struct {
	Graph        providers.GraphExecutionResult
	Plan         providers.PortableToolPlanV1
	Acquisitions []providers.PortableToolArtifactAcquisitionInputV1
}

// applicationPortablePythonSelectionSealV1 owns the one construction-time
// join between the selected catalog closures and their provider plan. All
// provider-facing views are derived from selection after this boundary.
type applicationPortablePythonSelectionSealV1 struct {
	documentDigest canonical.Digest
	selection      *pythonprovider.PortableToolPythonSelectionV1
	closures       []toolcatalog.SelectedClosureV1
	providerPlan   providers.ProviderPlanV1
	target         toolcatalog.TargetIdentityV1
	snapshot       toolcatalog.ImmutableOperationSnapshotV1
	baseBytes      []byte
	configBytes    []byte
}

type portablePythonAcquisitionCollectorV1 struct {
	mu     sync.Mutex
	inputs []providers.PortableToolArtifactAcquisitionInputV1
}

func (collector *portablePythonAcquisitionCollectorV1) add(handoffs []pythonprovider.PortableToolPythonVerifiedWheelHandoffV1) error {
	inputs := make([]providers.PortableToolArtifactAcquisitionInputV1, len(handoffs))
	for index, handoff := range handoffs {
		input, err := handoff.ArtifactAcquisitionInput()
		if err != nil {
			return fmt.Errorf("retain verified portable Python acquisition: %w", err)
		}
		inputs[index] = input
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	seen := make(map[string]struct{}, len(collector.inputs)+len(inputs))
	for _, input := range collector.inputs {
		seen[portablePythonAcquisitionTransportKeyV1(input)] = struct{}{}
	}
	for _, input := range inputs {
		key := portablePythonAcquisitionTransportKeyV1(input)
		if _, found := seen[key]; found {
			return fmt.Errorf("portable Python graph retained duplicate binding acquisition %q", input.Artifact.ID)
		}
		seen[key] = struct{}{}
	}
	collector.inputs = append(collector.inputs, inputs...)
	return nil
}

func portablePythonAcquisitionTransportKeyV1(input providers.PortableToolArtifactAcquisitionInputV1) string {
	return input.Scope + "\x00" + input.Tool + "\x00" + input.Artifact.ID
}

func (collector *portablePythonAcquisitionCollectorV1) snapshot() ([]providers.PortableToolArtifactAcquisitionInputV1, error) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.inputs) == 0 {
		return nil, fmt.Errorf("application portable Python graph retained no binding acquisitions")
	}
	seen := make(map[string]struct{}, len(collector.inputs))
	for _, input := range collector.inputs {
		key := portablePythonAcquisitionTransportKeyV1(input)
		if _, found := seen[key]; found {
			return nil, fmt.Errorf("portable Python graph retained duplicate binding acquisition %q", input.Artifact.ID)
		}
		seen[key] = struct{}{}
	}
	copyOfInputs := append([]providers.PortableToolArtifactAcquisitionInputV1{}, collector.inputs...)
	sort.Slice(copyOfInputs, func(i, j int) bool {
		left, right := copyOfInputs[i], copyOfInputs[j]
		if left.Scope != right.Scope {
			return left.Scope < right.Scope
		}
		if left.Tool != right.Tool {
			return left.Tool < right.Tool
		}
		return left.Artifact.ID < right.Artifact.ID
	})
	return copyOfInputs, nil
}

func cloneApplicationPortablePythonClosuresV1(closures []toolcatalog.SelectedClosureV1) ([]toolcatalog.SelectedClosureV1, error) {
	if closures == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(closures)
	if err != nil {
		return nil, fmt.Errorf("freeze application portable Python closures: %w", err)
	}
	var detached []toolcatalog.SelectedClosureV1
	if err := json.Unmarshal(encoded, &detached); err != nil {
		return nil, fmt.Errorf("freeze application portable Python closures: %w", err)
	}
	return detached, nil
}

func cloneApplicationPortablePythonProviderPlanV1(plan providers.ProviderPlanV1) (providers.ProviderPlanV1, error) {
	encoded, err := canonical.Marshal(plan)
	if err != nil {
		return providers.ProviderPlanV1{}, fmt.Errorf("freeze application portable Python provider plan: %w", err)
	}
	var detached providers.ProviderPlanV1
	if err := json.Unmarshal(encoded, &detached); err != nil {
		return providers.ProviderPlanV1{}, fmt.Errorf("freeze application portable Python provider plan: %w", err)
	}
	return detached, nil
}

func sealApplicationPortablePythonSelectionV1(
	selected *ApplicationPortableToolPlanV1,
	base deploy.ImageDescriptor,
	config providers.ImageConfigPolicy,
) (*applicationPortablePythonSelectionSealV1, error) {
	if selected == nil {
		return nil, fmt.Errorf("application portable Python graph requires a selected plan")
	}
	if err := base.Validate(); err != nil {
		return nil, fmt.Errorf("application portable Python preflight base: %w", err)
	}
	if err := providers.ValidateImageConfigPolicy(config); err != nil {
		return nil, fmt.Errorf("application portable Python preflight image config: %w", err)
	}
	if selected.Target.Platform != base.Platform.Canonical {
		return nil, fmt.Errorf("application portable Python target does not match preflight base platform")
	}
	baseBytes, err := canonical.Marshal(base)
	if err != nil {
		return nil, err
	}
	configBytes, err := canonical.Marshal(config)
	if err != nil {
		return nil, err
	}
	if err := providers.ValidatePortableToolProviderDAGV1(selected.DAG); err != nil {
		return nil, fmt.Errorf("application portable Python DAG: %w", err)
	}
	closures, err := cloneApplicationPortablePythonClosuresV1(selected.Closures)
	if err != nil {
		return nil, err
	}
	compiled, err := toolcatalog.CompilePortableToolPlanV1(closures)
	if err != nil {
		return nil, fmt.Errorf("application portable Python closures: %w", err)
	}
	compiledBytes, err := providers.CanonicalPortableToolPlanBytesV1(compiled)
	if err != nil {
		return nil, err
	}
	selectedBytes, err := providers.CanonicalPortableToolPlanBytesV1(selected.Plan)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(selectedBytes, compiledBytes) {
		return nil, fmt.Errorf("application portable Python closures do not match selected plan")
	}
	selection, err := pythonprovider.NewPortableToolPythonSelectionV1(compiled)
	if err != nil {
		return nil, err
	}
	sealedPlan, err := selection.Plan()
	if err != nil {
		return nil, err
	}
	sealedBytes, err := providers.CanonicalPortableToolPlanBytesV1(sealedPlan)
	if err != nil {
		return nil, err
	}
	dagBytes, err := providers.CanonicalPortableToolPlanBytesV1(selected.DAG.PortableToolPlan)
	if err != nil || !bytes.Equal(sealedBytes, dagBytes) {
		return nil, fmt.Errorf("application portable Python DAG does not match selected plan")
	}
	projection, err := selection.Projection()
	if err != nil {
		return nil, err
	}
	projectedBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(projection)
	if err != nil {
		return nil, err
	}
	providedBytes, err := pythonprovider.CanonicalPortableToolPythonProjectionBytesV1(selected.PythonProjection)
	if err != nil || !bytes.Equal(projectedBytes, providedBytes) {
		return nil, fmt.Errorf("application portable Python projection does not match selected plan")
	}
	providerPlan, err := cloneApplicationPortablePythonProviderPlanV1(selected.DAG.ProviderPlan)
	if err != nil {
		return nil, err
	}
	domains, err := applicationPortableProviderDomainsV1(sealedPlan, providerPlan)
	if err != nil {
		return nil, fmt.Errorf("application portable Python domains: %w", err)
	}
	expectedDAG, err := providers.BuildPortableToolProviderDAGV1(providerPlan, sealedPlan, domains)
	if err != nil {
		return nil, fmt.Errorf("application portable Python canonical DAG: %w", err)
	}
	expectedDAGBytes, err := providers.CanonicalPortableToolProviderDAGBytesV1(expectedDAG)
	if err != nil {
		return nil, err
	}
	actualDAGBytes, err := providers.CanonicalPortableToolProviderDAGBytesV1(selected.DAG)
	if err != nil || !bytes.Equal(expectedDAGBytes, actualDAGBytes) {
		return nil, fmt.Errorf("application portable Python DAG domains do not match canonical application ownership")
	}
	return &applicationPortablePythonSelectionSealV1{
		selection: selection, closures: closures, providerPlan: providerPlan,
		target: selected.Target, snapshot: selected.Snapshot,
		baseBytes: baseBytes, configBytes: configBytes,
	}, nil
}

func (sealed *applicationPortablePythonSelectionSealV1) freshPlan() (*PortableToolPythonFreshPlanV1, error) {
	if sealed == nil || sealed.selection == nil {
		return nil, fmt.Errorf("application portable Python selection seal is required")
	}
	plan, err := sealed.selection.Plan()
	if err != nil {
		return nil, err
	}
	closures := append([]toolcatalog.SelectedClosureV1{}, sealed.closures...)
	return &PortableToolPythonFreshPlanV1{
		Plan: plan, Closures: closures,
		sealed: &portableToolPythonFreshSelectionV1{selection: sealed.selection, closures: closures},
	}, nil
}

// ExecuteApplicationPortablePythonGraphV1 consumes the complete selected
// application preflight through the existing Python graph. A matching lock
// replays its exact wheels; otherwise the provider acquires the selected
// wheels after interpreter eligibility and before resolver staging.
func ExecuteApplicationPortablePythonGraphV1(
	ctx context.Context,
	selected *ApplicationPortableToolPlanV1,
	input PreparedPythonGraphExecutionInput,
) (ApplicationPortablePythonGraphResultV1, error) {
	if selected == nil || selected.sealed == nil {
		return ApplicationPortablePythonGraphResultV1{}, fmt.Errorf("application portable Python graph requires a sealed preflight selection")
	}
	sealed := selected.sealed
	if selected.Target != sealed.target || selected.Snapshot != sealed.snapshot {
		return ApplicationPortablePythonGraphResultV1{}, fmt.Errorf("application portable Python target or operation snapshot differs from sealed preflight")
	}
	baseBytes, err := canonical.Marshal(input.BaseDescriptor)
	if err != nil || !bytes.Equal(baseBytes, sealed.baseBytes) {
		return ApplicationPortablePythonGraphResultV1{}, fmt.Errorf("application portable Python execution base differs from preflight")
	}
	configBytes, err := canonical.Marshal(input.FinalImageConfig)
	if err != nil || !bytes.Equal(configBytes, sealed.configBytes) {
		return ApplicationPortablePythonGraphResultV1{}, fmt.Errorf("application portable Python execution image config differs from preflight")
	}
	if input.PortablePython != nil || input.DesiredPortableToolPlan != nil {
		return ApplicationPortablePythonGraphResultV1{}, fmt.Errorf("application portable Python graph input may not override selected bindings")
	}
	if input.Plan.Schema != "" {
		want, err := canonical.Marshal(sealed.providerPlan)
		if err != nil {
			return ApplicationPortablePythonGraphResultV1{}, err
		}
		got, err := canonical.Marshal(input.Plan)
		if err != nil || !bytes.Equal(want, got) {
			return ApplicationPortablePythonGraphResultV1{}, fmt.Errorf("application portable Python provider graph differs from selected DAG")
		}
	}
	input.Plan = sealed.providerPlan
	desiredPlan, err := sealed.selection.Plan()
	if err != nil {
		return ApplicationPortablePythonGraphResultV1{}, err
	}
	input.DesiredPortableToolPlan = &desiredPlan
	input.PortablePython, err = sealed.freshPlan()
	if err != nil {
		return ApplicationPortablePythonGraphResultV1{}, err
	}
	collector := &portablePythonAcquisitionCollectorV1{}
	input.portableAcquisitions = collector
	graph, err := ExecutePreparedPythonGraph(ctx, input)
	if err != nil {
		return ApplicationPortablePythonGraphResultV1{}, err
	}
	acquisitions, err := collector.snapshot()
	if err != nil {
		return ApplicationPortablePythonGraphResultV1{}, err
	}
	detachedPlan, err := sealed.selection.Plan()
	if err != nil {
		return ApplicationPortablePythonGraphResultV1{}, fmt.Errorf("copy selected portable Python plan: %w", err)
	}
	return ApplicationPortablePythonGraphResultV1{Graph: graph, Plan: detachedPlan, Acquisitions: acquisitions}, nil
}
