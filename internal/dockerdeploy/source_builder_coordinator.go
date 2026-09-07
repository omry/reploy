package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// SourceBuilderCoordinatorInputV1 fixes the build-wide authority used when a
// selected immutable source snapshot first declares portable build tools.
type SourceBuilderCoordinatorInputV1 struct {
	Store           providerstore.Store
	Base            deploy.ImageDescriptor
	ProviderPlan    providers.ProviderPlanV1
	BlueprintDigest canonical.Digest
	ReployVersion   string
	RunOptions      RunOptions
}

// SourceBuilderCoordinatorV1 owns selection-triggered portable-tool planning,
// acquisition, offline materialization, and disposable builder preparation.
// Python supplies only path-free recipes read from snapshots dependency
// resolution already selected; it never solves or materializes a tool.
type SourceBuilderCoordinatorV1 struct {
	input     SourceBuilderCoordinatorInputV1
	mu        sync.Mutex
	recipes   map[string]SourceBuilderSelectedRecipeV1
	tools     []*SourceBuilderPortableToolsV1
	latest    *SourceBuilderPortableToolsV1
	cleanedUp bool
}

var planSelectedSourceBuilderPortableToolsV1 = PlanSourceBuilderPortableToolsV1
var materializeSelectedSourceBuilderPortableToolsV1 = MaterializeSourceBuilderPortableToolsV1
var prepareSelectedSourceBuilderEnvironmentV1 = PrepareSourceBuilderEnvironmentV1

func NewSourceBuilderCoordinatorV1(input SourceBuilderCoordinatorInputV1) *SourceBuilderCoordinatorV1 {
	return &SourceBuilderCoordinatorV1{
		input:   input,
		recipes: map[string]SourceBuilderSelectedRecipeV1{},
	}
}

// Prepare merges newly selected immutable recipes into the operation
// snapshot, resolves and materializes the complete selected closure, and
// prepares a validated disposable builder from upstream. Calls are serialized
// so concurrently ready Python nodes cannot observe a partial union.
func (coordinator *SourceBuilderCoordinatorV1) Prepare(
	ctx context.Context,
	upstream deploy.ImageDescriptor,
	selected []SourceBuilderSelectedRecipeV1,
) (*SourceBuilderEnvironmentV1, error) {
	if coordinator == nil {
		return nil, fmt.Errorf("prepare source-builder tools requires a coordinator")
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("prepare source-builder tools requires selected recipes")
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.cleanedUp {
		return nil, fmt.Errorf("source-builder coordinator is already cleaned up")
	}
	for _, item := range selected {
		item = cloneSourceBuilderSelectedRecipeV1(item)
		if prior, found := coordinator.recipes[item.Distribution]; found && !reflect.DeepEqual(prior, item) {
			return nil, fmt.Errorf("selected local source recipe for %q differs across provider nodes", item.Distribution)
		}
		coordinator.recipes[item.Distribution] = item
	}
	all := make([]SourceBuilderSelectedRecipeV1, 0, len(coordinator.recipes))
	for _, item := range coordinator.recipes {
		all = append(all, item)
	}
	sort.Slice(all, func(left, right int) bool { return all[left].Distribution < all[right].Distribution })
	plan, err := planSelectedSourceBuilderPortableToolsV1(ctx, PlanSourceBuilderPortableToolsInputV1{
		Store: coordinator.input.Store, Base: coordinator.input.Base,
		ProviderPlan: coordinator.input.ProviderPlan, SelectedRecipes: all,
		BlueprintDigest: coordinator.input.BlueprintDigest, ReployVersion: coordinator.input.ReployVersion,
	})
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("selected source-builder recipes declare no portable tools")
	}
	tools, err := materializeSelectedSourceBuilderPortableToolsV1(ctx, coordinator.input.Store, plan)
	if err != nil {
		return nil, err
	}
	environment, err := prepareSelectedSourceBuilderEnvironmentV1(
		ctx, coordinator.input.Store, tools, upstream, coordinator.input.RunOptions,
	)
	if err != nil {
		return nil, errors.Join(err, tools.Cleanup())
	}
	coordinator.tools = append(coordinator.tools, tools)
	coordinator.latest = tools
	return environment, nil
}

// PortableToolLock returns the complete selected lock after graph execution.
func (coordinator *SourceBuilderCoordinatorV1) PortableToolLock() *providers.PortableToolLockV1 {
	if coordinator == nil {
		return nil
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.latest == nil {
		return nil
	}
	lock := providers.ClonePortableToolLockV1(coordinator.latest.Lock)
	return &lock
}

// Cleanup removes every host materialization tree accumulated while selected
// recipe scopes expanded. Disposable builder images have separate ownership.
func (coordinator *SourceBuilderCoordinatorV1) Cleanup() error {
	if coordinator == nil {
		return nil
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.cleanedUp {
		return nil
	}
	coordinator.cleanedUp = true
	var result error
	for index := len(coordinator.tools) - 1; index >= 0; index-- {
		result = errors.Join(result, coordinator.tools[index].Cleanup())
	}
	return result
}
