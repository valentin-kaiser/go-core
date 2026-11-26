package mail

const (
	// PriorityLow represents low priority
	PriorityLow Priority = iota
	// PriorityNormal represents normal priority
	PriorityNormal
	// PriorityHigh represents high priority
	PriorityHigh
	// PriorityCritical represents critical priority
	PriorityCritical
)

// Priority represents the message priority
type Priority int

// String returns the string representation of the priority
func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityCritical:
		return "critical"
	default:
		return "unknown"
	}
}
