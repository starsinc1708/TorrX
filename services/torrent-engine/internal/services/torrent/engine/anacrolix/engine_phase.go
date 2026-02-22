package anacrolix

import "torrentstream/internal/domain"

func deriveTransferPhase(
	status domain.TorrentStatus,
	mode domain.SessionMode,
	stableCompleted int64,
	verifiedCompleted int64,
) (domain.TransferPhase, float64) {
	if status != domain.TorrentActive {
		return "", 0
	}

	switch mode {
	case domain.ModeStopped, domain.ModeCompleted:
		return "", 0
	}

	if stableCompleted > 0 && verifiedCompleted >= 0 && verifiedCompleted < stableCompleted {
		progress := float64(verifiedCompleted) / float64(stableCompleted)
		if progress < 0 {
			progress = 0
		}
		if progress > 1 {
			progress = 1
		}
		return domain.TransferPhaseVerifying, progress
	}

	return domain.TransferPhaseDownloading, 0
}
