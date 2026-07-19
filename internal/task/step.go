package task

type StepState string

const (
	StepPending   StepState = "pending"
	StepRunning   StepState = "running"
	StepRetryWait StepState = "retry_wait"
	StepSucceeded StepState = "succeeded"
	StepSkipped   StepState = "skipped"
	StepFailed    StepState = "failed"
)

func (s StepState) Terminal() bool {
	switch s {
	case StepSucceeded, StepSkipped, StepFailed:
		return true
	default:
		return false
	}
}
