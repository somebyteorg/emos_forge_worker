package task

import "fmt"

type State string

const (
	StateDiscovered             State = "discovered"
	StatePreparing              State = "preparing"
	StateDownloading            State = "downloading"
	StateProbing                State = "probing"
	StateValidating             State = "validating"
	StateProcessing             State = "processing"
	StatePackaging              State = "packaging"
	StateValidatingOutput       State = "validating_output"
	StateFinalizing             State = "finalizing"
	StateRetryWait              State = "retry_wait"
	StateSucceeded              State = "succeeded"
	StateFailed                 State = "failed"
	StateFailedUnsupportedMedia State = "failed_unsupported_media"
)

var normalTransitions = map[State]map[State]struct{}{
	StateDiscovered:       set(StatePreparing),
	StatePreparing:        set(StateDownloading, StateProbing, StateRetryWait),
	StateDownloading:      set(StateProbing, StateRetryWait),
	StateProbing:          set(StateValidating, StateRetryWait, StateFailedUnsupportedMedia),
	StateValidating:       set(StateProcessing, StateRetryWait, StateFailedUnsupportedMedia),
	StateProcessing:       set(StatePackaging, StateValidatingOutput, StateRetryWait, StateFailedUnsupportedMedia),
	StatePackaging:        set(StateValidatingOutput, StateRetryWait),
	StateValidatingOutput: set(StateFinalizing, StateRetryWait),
	StateFinalizing:       set(StateSucceeded, StateRetryWait),
	StateRetryWait: set(
		StatePreparing, StateDownloading, StateProbing, StateValidating,
		StateProcessing, StatePackaging, StateValidatingOutput, StateFinalizing,
	),
}

func set(states ...State) map[State]struct{} {
	result := make(map[State]struct{}, len(states))
	for _, state := range states {
		result[state] = struct{}{}
	}
	return result
}

func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateFailedUnsupportedMedia
}

func CanTransition(from, to State) bool {
	if from.Terminal() || from == to {
		return false
	}
	if to == StateFailed || to == StateFailedUnsupportedMedia {
		return true
	}
	_, ok := normalTransitions[from][to]
	return ok
}

func ValidateTransition(from, to State) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("illegal task state transition %q -> %q", from, to)
	}
	return nil
}
