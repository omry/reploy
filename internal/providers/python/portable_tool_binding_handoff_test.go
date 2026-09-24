package python

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPortableToolPythonSelectionHandoffFreshAndDetached(t *testing.T) {
	request, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	plan := providers.PortableToolPlanV1{Schema: providers.PortableToolPlanSchemaV1, Tools: []providers.PortableToolPlanEntryV1{request.SelectedPlanEntry}}
	selection, err := NewPortableToolPythonSelectionV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := selection.Projection()
	if err != nil {
		t.Fatal(err)
	}
	projection.Components[0].Bindings[0].Distribution = "substituted"
	plan.Tools[0].Scope = "application:substituted"
	selected, err := selection.Binding(request.Binding.Component, request.Binding.Distribution)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := selected.FreshWheel(request.ManifestRecord, request.SourceRecord, request.Interpreter)
	if err != nil {
		t.Fatal(err)
	}
	request.SourceRecord.Record.Value["mirrors"].([]any)[0] = "https://example.invalid/substituted"
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	verified, err := ProducePortableToolPythonSelectedWheelsV1(context.Background(), store, selection, request.Binding.Component, []*PortableToolPythonWheelHandoffV1{handoff})
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 {
		t.Fatalf("verified wheels = %#v", verified)
	}
	materialization, err := verified[0].MaterializationInput()
	if err != nil || materialization.Descriptor != descriptor {
		t.Fatalf("materialization input = %#v, error = %v", materialization, err)
	}
	materialization.Inspection.InternalTags[0] = "substituted"
	again, err := verified[0].MaterializationInput()
	if err != nil || again.Inspection.InternalTags[0] == "substituted" {
		t.Fatalf("verified handoff aliases materialization input: %#v, error = %v", again, err)
	}
	lockInput, err := verified[0].ArtifactAcquisitionInput()
	if err != nil || lockInput.Descriptor != descriptor {
		t.Fatalf("lock input = %#v, error = %v", lockInput, err)
	}
}

func TestPortableToolPythonSelectionHandoffRejectsMisuseBeforeStore(t *testing.T) {
	request, _, _ := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelFreshV1)
	plan := providers.PortableToolPlanV1{Schema: providers.PortableToolPlanSchemaV1, Tools: []providers.PortableToolPlanEntryV1{request.SelectedPlanEntry}}
	selection, err := NewPortableToolPythonSelectionV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selection.Binding(request.Binding.Component, request.Binding.Distribution)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selection.Binding(request.Binding.Component, "substituted"); err == nil {
		t.Fatal("substituted distribution accepted")
	}
	handoff, err := selected.FreshWheel(request.ManifestRecord, request.SourceRecord, request.Interpreter)
	if err != nil {
		t.Fatal(err)
	}
	for name, handoffs := range map[string][]*PortableToolPythonWheelHandoffV1{
		"omitted": {}, "duplicate": {handoff, handoff},
	} {
		_, err := ProducePortableToolPythonSelectedWheelsV1(context.Background(), providerstore.Store{}, selection, request.Binding.Component, handoffs)
		if err == nil {
			t.Fatalf("%s handoffs accepted", name)
		}
	}
	other, err := NewPortableToolPythonSelectionV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ProducePortableToolPythonSelectedWheelsV1(context.Background(), providerstore.Store{}, other, request.Binding.Component, []*PortableToolPythonWheelHandoffV1{handoff})
	if err == nil || !strings.Contains(err.Error(), "not from this selection") {
		t.Fatalf("misattributed handoff error = %v", err)
	}
	badSource := request.SourceRecord
	badSource.Reference.ID = "substituted"
	if _, err := selected.FreshWheel(request.ManifestRecord, badSource, request.Interpreter); err == nil {
		t.Fatal("substituted source accepted")
	}
}

func TestPortableToolPythonSelectionHandoffLockedReplayUsesStore(t *testing.T) {
	request, descriptor, content := portableToolVerifiedWheelRequestFixtureV1(t, PortableToolVerifiedWheelLockedReplayV1)
	plan := providers.PortableToolPlanV1{Schema: providers.PortableToolPlanSchemaV1, Tools: []providers.PortableToolPlanEntryV1{request.SelectedPlanEntry}}
	selection, err := NewPortableToolPythonSelectionV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selection.Binding(request.Binding.Component, request.Binding.Distribution)
	if err != nil {
		t.Fatal(err)
	}
	wrongDescriptor := *request.LockedAcquisition
	wrongDescriptor.Descriptor.LogicalPath = "wheels/substituted.whl"
	if _, err := selected.LockedWheel(request.ManifestRecord, wrongDescriptor, request.Interpreter); err == nil {
		t.Fatal("substituted replay descriptor accepted")
	}
	wrongSource := *request.LockedAcquisition
	wrongSource.Source.Reference.ID = "substituted"
	if _, err := selected.LockedWheel(request.ManifestRecord, wrongSource, request.Interpreter); err == nil {
		t.Fatal("misattributed replay source accepted")
	}
	handoff, err := selected.LockedWheel(request.ManifestRecord, *request.LockedAcquisition, request.Interpreter)
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProducePortableToolPythonSelectedWheelsV1(context.Background(), store, selection, request.Binding.Component, []*PortableToolPythonWheelHandoffV1{handoff}); err == nil {
		t.Fatal("replay accepted absent store bytes")
	}
	if _, err := store.PublishExpected(context.Background(), descriptor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	verified, err := ProducePortableToolPythonSelectedWheelsV1(context.Background(), store, selection, request.Binding.Component, []*PortableToolPythonWheelHandoffV1{handoff})
	if err != nil || len(verified) != 1 {
		t.Fatalf("locked replay wheels = %#v, error = %v", verified, err)
	}
}
