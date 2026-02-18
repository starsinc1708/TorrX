package domain

type Priority int

const (
	PriorityNone      Priority = -1
	PriorityLow       Priority = 0
	PriorityNormal    Priority = 1
	PriorityReadahead Priority = 2 // Within readahead window — maps to PiecePriorityReadahead.
	PriorityNext      Priority = 3 // Very next piece to be consumed — maps to PiecePriorityNext.
	PriorityHigh      Priority = 4 // Immediate need — maps to PiecePriorityNow.
)
