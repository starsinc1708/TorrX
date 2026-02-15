package domain

import "errors"

// SessionMode represents the engine-internal runtime state of a torrent session.
// It is distinct from TorrentStatus which is the persisted state in the database.
type SessionMode string

const (
	ModeIdle        SessionMode = "idle"        // Metadata not yet available.
	ModeDownloading SessionMode = "downloading"  // Actively downloading.
	ModeStopped     SessionMode = "stopped"      // User stopped.
	ModeFocused     SessionMode = "focused"      // Current torrent, gets 100% bandwidth.
	ModePaused      SessionMode = "paused"       // Scheduler paused (another torrent is focused).
	ModeCompleted   SessionMode = "completed"    // Download finished.
)

var ErrInvalidTransition = errors.New("invalid state transition")

// validTransitions defines the adjacency list of allowed state transitions.
var validTransitions = map[SessionMode][]SessionMode{
	ModeIdle:        {ModeDownloading, ModePaused, ModeStopped},
	ModeDownloading: {ModeStopped, ModeFocused, ModePaused, ModeCompleted},
	ModeFocused:     {ModeDownloading, ModeStopped, ModeCompleted},
	ModePaused:      {ModeDownloading, ModeFocused, ModeStopped},
	ModeStopped:     {ModeDownloading, ModePaused, ModeIdle},
	ModeCompleted:   {ModeStopped, ModeFocused},
}

// CanTransition reports whether a transition from one mode to another is valid.
func CanTransition(from, to SessionMode) bool {
	for _, t := range validTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// ToStatus maps the engine-internal SessionMode to the persisted TorrentStatus.
func (m SessionMode) ToStatus() TorrentStatus {
	switch m {
	case ModeIdle:
		return TorrentPending
	case ModeDownloading, ModeFocused, ModePaused:
		return TorrentActive
	case ModeStopped:
		return TorrentStopped
	case ModeCompleted:
		return TorrentCompleted
	default:
		return TorrentError
	}
}
