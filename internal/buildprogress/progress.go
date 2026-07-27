package buildprogress

// Phase identifies one stable, user-facing portion of an environment build.
type Phase uint8

const (
	PhaseInspect Phase = iota
	PhasePrepare
	PhaseProviders
	PhaseAssemble
	PhasePublish
)

const PhaseCount = 5

// Event reports determinate work within one stable build phase. Completed and
// Total are optional; a zero Total means the phase has no useful sub-step
// denominator yet.
type Event struct {
	Phase       Phase
	Environment string
	Detail      string
	Completed   int
	Total       int
}

// Reporter receives structured build progress. Reporters must return quickly;
// build execution calls them synchronously.
type Reporter func(Event)

func Report(reporter Reporter, event Event) {
	if reporter != nil {
		reporter(event)
	}
}
