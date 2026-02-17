package apihttp

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"torrentstream/internal/metrics"
)

// PlaybackState represents the current state of an HLS encoding job.
type PlaybackState int

const (
	StateIdle       PlaybackState = iota
	StateStarting                 // FFmpeg about to launch
	StateBuffering                // First segments being produced
	StatePlaying                  // Steady-state encoding
	StateSeeking                  // User-initiated seek in progress
	StateStalled                  // No new segments for too long
	StateRestarting               // Auto-restart triggered
	StateCompleted                // FFmpeg finished encoding the whole file
	StateError                    // Terminal error
)

var stateNames = [...]string{
	"idle", "starting", "buffering", "playing",
	"seeking", "stalled", "restarting", "completed", "error",
}

func (s PlaybackState) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return fmt.Sprintf("unknown(%d)", int(s))
}

// validTransitions defines the allowed state transitions.
var validTransitions = map[PlaybackState][]PlaybackState{
	StateIdle:       {StateStarting},
	StateStarting:   {StateBuffering, StateError},
	StateBuffering:  {StatePlaying, StateError, StateStalled},
	StatePlaying:    {StateSeeking, StateStalled, StateCompleted, StateError},
	StateSeeking:    {StateStarting, StateError},
	StateStalled:    {StateRestarting, StateError},
	StateRestarting: {StateStarting, StateError},
	StateCompleted:  {StateSeeking, StateIdle},
	StateError:      {StateIdle, StateStarting},
}

func canTransition(from, to PlaybackState) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// PlaybackController manages the state machine for a single HLS job.
type PlaybackController struct {
	mu         sync.RWMutex
	state      PlaybackState
	generation uint64
	err        error
	stateTime  time.Time
	listeners  []func(from, to PlaybackState)
}

// NewPlaybackController creates a controller in the Idle state.
func NewPlaybackController() *PlaybackController {
	return &PlaybackController{
		state:     StateIdle,
		stateTime: time.Now(),
	}
}

// State returns the current playback state.
func (c *PlaybackController) State() PlaybackState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Generation returns the current generation counter (incremented on seek).
func (c *PlaybackController) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// Err returns the last error, if any.
func (c *PlaybackController) Err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}

// StateDuration returns how long the controller has been in the current state.
func (c *PlaybackController) StateDuration() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.stateTime)
}

// Transition attempts to move from the current state to the target state.
// Returns an error if the transition is not valid.
func (c *PlaybackController) Transition(to PlaybackState) error {
	c.mu.Lock()
	from := c.state
	if !canTransition(from, to) {
		c.mu.Unlock()
		return fmt.Errorf("invalid playback state transition: %s -> %s", from, to)
	}
	c.state = to
	c.stateTime = time.Now()
	if to != StateError {
		c.err = nil
	}
	listeners := make([]func(from, to PlaybackState), len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()

	metrics.HLSStateTransitionsTotal.WithLabelValues(from.String(), to.String()).Inc()

	for _, fn := range listeners {
		fn(from, to)
	}
	return nil
}

// TransitionWithError moves to StateError and records the error.
func (c *PlaybackController) TransitionWithError(err error) error {
	c.mu.Lock()
	from := c.state
	if !canTransition(from, StateError) {
		c.mu.Unlock()
		return fmt.Errorf("invalid playback state transition: %s -> error", from)
	}
	c.state = StateError
	c.stateTime = time.Now()
	c.err = err
	listeners := make([]func(from, to PlaybackState), len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()

	metrics.HLSStateTransitionsTotal.WithLabelValues(from.String(), StateError.String()).Inc()

	for _, fn := range listeners {
		fn(from, StateError)
	}
	return nil
}

// IncrementGeneration increments the generation counter (called on seek).
func (c *PlaybackController) IncrementGeneration() uint64 {
	c.mu.Lock()
	c.generation++
	gen := c.generation
	c.mu.Unlock()
	return gen
}

// OnTransition registers a listener that is called after every state change.
func (c *PlaybackController) OnTransition(fn func(from, to PlaybackState)) {
	c.mu.Lock()
	c.listeners = append(c.listeners, fn)
	c.mu.Unlock()
}

// IsRunning returns true if the job is in an active encoding state.
func (c *PlaybackController) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state == StateStarting || c.state == StateBuffering || c.state == StatePlaying
}

// IsTerminal returns true if the job is in a terminal state (completed or error).
func (c *PlaybackController) IsTerminal() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state == StateCompleted || c.state == StateError
}

// generationRef is an atomic uint64 that can be shared between the HLS job
// and the sliding priority reader to detect stale readers from pre-seek.
type generationRef struct {
	val atomic.Uint64
}

func newGenerationRef(initial uint64) *generationRef {
	g := &generationRef{}
	g.val.Store(initial)
	return g
}

func (g *generationRef) Load() uint64  { return g.val.Load() }
func (g *generationRef) Store(v uint64) { g.val.Store(v) }
