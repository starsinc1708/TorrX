package mongo

import "torrentstream/internal/domain/ports"

var _ ports.TorrentRepository = (*Repository)(nil)
