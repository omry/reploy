package dockerdeploy

import (
	"fmt"
	"strconv"

	"github.com/omry/reploy/internal/controlledsession"
)

// PrepareControlledSessionChannelV1 realizes only the private controller
// channel from a complete immutable execution plan. It does not create or
// start either container.
func PrepareControlledSessionChannelV1(plan ControlledSessionExecutionPlanV1) (*controlledsession.PrivateChannelV1, error) {
	config, err := controlledSessionPrivateChannelConfigV1(plan)
	if err != nil {
		return nil, err
	}
	channel, err := controlledsession.PreparePrivateChannelV1(config)
	if err != nil {
		return nil, fmt.Errorf("prepare planned controlled-session channel: %w", err)
	}
	return channel, nil
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
			Columns:                               uint32(columns),
			Rows:                                  uint32(rows),
			OutputFinalizationTimeoutMilliseconds: controlledsession.DefaultOutputFinalizationTimeoutMillisecondsV1,
		},
	}, nil
}
