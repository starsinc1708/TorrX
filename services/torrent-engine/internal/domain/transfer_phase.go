package domain

// TransferPhase describes what the data plane is currently doing for an
// active torrent session.
type TransferPhase string

const (
	TransferPhaseDownloading TransferPhase = "downloading"
	TransferPhaseVerifying   TransferPhase = "verifying"
)
