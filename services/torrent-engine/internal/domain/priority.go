package domain

type Priority int

const (
	PriorityNone   Priority = -1
	PriorityLow    Priority = 0
	PriorityNormal Priority = 1
	PriorityHigh   Priority = 2
)
