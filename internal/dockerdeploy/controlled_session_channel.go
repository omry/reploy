package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/controlledsession"
)

// PrepareControlledSessionChannelV1 realizes only the private controller
// channel from a complete immutable execution plan. It does not create or
// start either container.
func PrepareControlledSessionChannelV1(plan ControlledSessionExecutionPlanV1) (*controlledsession.PrivateChannelV1, error) {
	if plan.Controller.SessionNetwork.Enabled {
		return nil, fmt.Errorf("prepare controlled-session channel: realized session-network prefixes are required")
	}
	return prepareControlledSessionChannelWithNetworkV1(plan, controlledSessionNetworkRealizationV1{})
}

func prepareControlledSessionChannelWithNetworkV1(plan ControlledSessionExecutionPlanV1, realized controlledSessionNetworkRealizationV1) (*controlledsession.PrivateChannelV1, error) {
	config, err := controlledSessionPrivateChannelConfigV1(plan)
	if err != nil {
		return nil, err
	}
	channel, err := controlledsession.PreparePrivateChannelV1(config)
	if err != nil {
		return nil, fmt.Errorf("prepare planned controlled-session channel: %w", err)
	}
	if err := writeControlledSessionNetworkPolicyV1(plan, realized); err != nil {
		return nil, errors.Join(err, channel.Close())
	}
	return channel, nil
}

func writeControlledSessionNetworkPolicyV1(plan ControlledSessionExecutionPlanV1, realized controlledSessionNetworkRealizationV1) error {
	if !plan.Controller.SessionNetwork.Enabled {
		if len(realized.Subnets) != 0 || len(realized.ControllerAddresses) != 0 || len(realized.WorkloadAddresses) != 0 {
			return fmt.Errorf("prepare controlled-session channel: disabled session network received realized prefixes")
		}
		return nil
	}
	want, err := deriveControlledSessionNetworkRealizationV1(realized.Subnets)
	if err != nil {
		return fmt.Errorf("prepare controlled-session channel: %w", err)
	}
	if !slices.Equal(realized.ControllerAddresses, want.ControllerAddresses) ||
		!slices.Equal(realized.WorkloadAddresses, want.WorkloadAddresses) {
		return fmt.Errorf("prepare controlled-session channel: participant addresses do not match the realized prefixes")
	}
	prefixes := slices.Clone(realized.Subnets)
	sort.Strings(prefixes)
	if len(prefixes) == 0 {
		return fmt.Errorf("prepare controlled-session channel: enabled session network has no realized prefixes")
	}
	lines := make([]string, 0, len(prefixes)+len(realized.ControllerAddresses)+len(realized.WorkloadAddresses))
	for _, prefix := range prefixes {
		lines = append(lines, "prefix "+prefix)
	}
	for _, address := range realized.ControllerAddresses {
		lines = append(lines, "controller "+address)
	}
	for _, address := range realized.WorkloadAddresses {
		lines = append(lines, "workload "+address)
	}
	policyPath := filepath.Join(plan.Channel.HostDirectory, controlledSessionNetworkPolicyFileNameV1)
	file, err := os.OpenFile(policyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return fmt.Errorf("create controlled-session network policy input: %w", err)
	}
	_, writeErr := file.WriteString(strings.Join(lines, "\n") + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("write controlled-session network policy input: %w", errors.Join(writeErr, closeErr)), os.Remove(policyPath))
	}
	return nil
}

func controlledSessionPrivateChannelConfigV1(plan ControlledSessionExecutionPlanV1) (controlledsession.PrivateChannelConfigV1, error) {
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		return controlledsession.PrivateChannelConfigV1{}, fmt.Errorf("prepare controlled-session channel plan: %w", err)
	}
	columns, err := strconv.ParseUint(plan.Workload.InitialColumns, 10, 32)
	if err != nil {
		return controlledsession.PrivateChannelConfigV1{}, fmt.Errorf("prepare controlled-session channel initial columns: %w", err)
	}
	rows, err := strconv.ParseUint(plan.Workload.InitialRows, 10, 32)
	if err != nil {
		return controlledsession.PrivateChannelConfigV1{}, fmt.Errorf("prepare controlled-session channel initial rows: %w", err)
	}
	return controlledsession.PrivateChannelConfigV1{
		HostDirectory: plan.Channel.HostDirectory,
		Opened: controlledsession.OpenedV1{
			Authorization:                         plan.Authorization,
			Endpoints:                             controlledSessionOpenedEndpointsV1(plan.Controller.SessionNetwork.Endpoints),
			Columns:                               uint32(columns),
			Rows:                                  uint32(rows),
			OutputFinalizationTimeoutMilliseconds: controlledsession.DefaultOutputFinalizationTimeoutMillisecondsV1,
		},
	}, nil
}
