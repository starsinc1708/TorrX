package apihttp

import (
	"errors"
	"sync"
	"testing"
)

func TestPlaybackControllerInitialState(t *testing.T) {
	ctrl := NewPlaybackController()
	if ctrl.State() != StateIdle {
		t.Fatalf("expected initial state Idle, got %s", ctrl.State())
	}
}

func TestPlaybackControllerValidTransitions(t *testing.T) {
	for from, targets := range validTransitions {
		for _, to := range targets {
			t.Run(from.String()+"->"+to.String(), func(t *testing.T) {
				ctrl := NewPlaybackController()
				// Drive the controller to the "from" state by walking a path from Idle.
				if err := driveToState(ctrl, from); err != nil {
					t.Fatalf("failed to reach state %s: %v", from, err)
				}
				if err := ctrl.Transition(to); err != nil {
					t.Fatalf("expected valid transition %s -> %s, got error: %v", from, to, err)
				}
				if ctrl.State() != to {
					t.Fatalf("expected state %s after transition, got %s", to, ctrl.State())
				}
			})
		}
	}
}

func TestPlaybackControllerInvalidTransitions(t *testing.T) {
	allStates := []PlaybackState{
		StateIdle, StateStarting, StateBuffering, StatePlaying,
		StateSeeking, StateStalled, StateRestarting, StateCompleted, StateError,
	}

	cases := []struct {
		from PlaybackState
		to   PlaybackState
	}{}
	for _, from := range allStates {
		allowed := make(map[PlaybackState]bool)
		for _, a := range validTransitions[from] {
			allowed[a] = true
		}
		for _, to := range allStates {
			if !allowed[to] {
				cases = append(cases, struct {
					from PlaybackState
					to   PlaybackState
				}{from, to})
			}
		}
	}

	for _, tc := range cases {
		t.Run(tc.from.String()+"->"+tc.to.String(), func(t *testing.T) {
			ctrl := NewPlaybackController()
			if err := driveToState(ctrl, tc.from); err != nil {
				t.Fatalf("failed to reach state %s: %v", tc.from, err)
			}
			if err := ctrl.Transition(tc.to); err == nil {
				t.Fatalf("expected error for invalid transition %s -> %s, but got nil", tc.from, tc.to)
			}
		})
	}
}

func TestPlaybackControllerTransitionWithError(t *testing.T) {
	ctrl := NewPlaybackController()
	// Idle -> Starting (valid)
	if err := ctrl.Transition(StateStarting); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Starting -> Error via TransitionWithError
	origErr := errors.New("ffmpeg crashed")
	if err := ctrl.TransitionWithError(origErr); err != nil {
		t.Fatalf("unexpected error from TransitionWithError: %v", err)
	}
	if ctrl.State() != StateError {
		t.Fatalf("expected state Error, got %s", ctrl.State())
	}
	if ctrl.Err() != origErr {
		t.Fatalf("expected stored error %v, got %v", origErr, ctrl.Err())
	}

	// After transitioning away from error, err should be cleared.
	if err := ctrl.Transition(StateIdle); err != nil {
		t.Fatalf("unexpected error transitioning from Error to Idle: %v", err)
	}
	if ctrl.Err() != nil {
		t.Fatalf("expected nil error after leaving Error state, got %v", ctrl.Err())
	}
}

func TestPlaybackControllerGeneration(t *testing.T) {
	ctrl := NewPlaybackController()
	if ctrl.Generation() != 0 {
		t.Fatalf("expected initial generation 0, got %d", ctrl.Generation())
	}

	g1 := ctrl.IncrementGeneration()
	if g1 != 1 {
		t.Fatalf("expected generation 1, got %d", g1)
	}
	g2 := ctrl.IncrementGeneration()
	if g2 != 2 {
		t.Fatalf("expected generation 2, got %d", g2)
	}
	if ctrl.Generation() != 2 {
		t.Fatalf("expected Generation() to return 2, got %d", ctrl.Generation())
	}
}

func TestPlaybackControllerListener(t *testing.T) {
	ctrl := NewPlaybackController()

	var mu sync.Mutex
	var transitions []struct{ from, to PlaybackState }

	ctrl.OnTransition(func(from, to PlaybackState) {
		mu.Lock()
		transitions = append(transitions, struct{ from, to PlaybackState }{from, to})
		mu.Unlock()
	})

	if err := ctrl.Transition(StateStarting); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ctrl.Transition(StateBuffering); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(transitions))
	}
	if transitions[0].from != StateIdle || transitions[0].to != StateStarting {
		t.Fatalf("expected first transition Idle->Starting, got %s->%s",
			transitions[0].from, transitions[0].to)
	}
	if transitions[1].from != StateStarting || transitions[1].to != StateBuffering {
		t.Fatalf("expected second transition Starting->Buffering, got %s->%s",
			transitions[1].from, transitions[1].to)
	}
}

func TestPlaybackControllerIsRunning(t *testing.T) {
	tests := []struct {
		state    PlaybackState
		expected bool
	}{
		{StateIdle, false},
		{StateStarting, true},
		{StateBuffering, true},
		{StatePlaying, true},
		{StateSeeking, false},
		{StateStalled, false},
		{StateRestarting, false},
		{StateCompleted, false},
		{StateError, false},
	}

	for _, tc := range tests {
		t.Run(tc.state.String(), func(t *testing.T) {
			ctrl := NewPlaybackController()
			if err := driveToState(ctrl, tc.state); err != nil {
				t.Fatalf("failed to reach state %s: %v", tc.state, err)
			}
			if ctrl.IsRunning() != tc.expected {
				t.Fatalf("expected IsRunning()=%v for state %s, got %v",
					tc.expected, tc.state, ctrl.IsRunning())
			}
		})
	}
}

func TestPlaybackControllerIsTerminal(t *testing.T) {
	tests := []struct {
		state    PlaybackState
		expected bool
	}{
		{StateIdle, false},
		{StateStarting, false},
		{StateBuffering, false},
		{StatePlaying, false},
		{StateSeeking, false},
		{StateStalled, false},
		{StateRestarting, false},
		{StateCompleted, true},
		{StateError, true},
	}

	for _, tc := range tests {
		t.Run(tc.state.String(), func(t *testing.T) {
			ctrl := NewPlaybackController()
			if err := driveToState(ctrl, tc.state); err != nil {
				t.Fatalf("failed to reach state %s: %v", tc.state, err)
			}
			if ctrl.IsTerminal() != tc.expected {
				t.Fatalf("expected IsTerminal()=%v for state %s, got %v",
					tc.expected, tc.state, ctrl.IsTerminal())
			}
		})
	}
}

func TestGenerationRef(t *testing.T) {
	ref := newGenerationRef(42)
	if ref.Load() != 42 {
		t.Fatalf("expected initial value 42, got %d", ref.Load())
	}
	ref.Store(100)
	if ref.Load() != 100 {
		t.Fatalf("expected stored value 100, got %d", ref.Load())
	}

	// Verify atomicity under concurrent access.
	var wg sync.WaitGroup
	ref.Store(0)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				ref.Store(ref.Load() + 1)
			}
		}()
	}
	wg.Wait()
	// We cannot assert an exact value due to races, but Load should not panic.
	_ = ref.Load()
}

// driveToState drives a PlaybackController from Idle to the desired state
// via the shortest valid transition path.
func driveToState(ctrl *PlaybackController, target PlaybackState) error {
	if ctrl.State() == target {
		return nil
	}

	// BFS to find shortest path from Idle to target.
	type node struct {
		state PlaybackState
		path  []PlaybackState
	}
	visited := make(map[PlaybackState]bool)
	queue := []node{{state: StateIdle, path: nil}}
	visited[StateIdle] = true

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, next := range validTransitions[curr.state] {
			newPath := append(append([]PlaybackState{}, curr.path...), next)
			if next == target {
				// Walk the path.
				for _, s := range newPath {
					if s == StateError {
						if err := ctrl.TransitionWithError(errors.New("test error")); err != nil {
							return err
						}
					} else {
						if err := ctrl.Transition(s); err != nil {
							return err
						}
					}
				}
				return nil
			}
			if !visited[next] {
				visited[next] = true
				queue = append(queue, node{state: next, path: newPath})
			}
		}
	}

	return errors.New("no valid path from Idle to " + target.String())
}
