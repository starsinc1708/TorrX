package anacrolix

import "torrentstream/internal/domain/ports"

var _ ports.Engine = (*Engine)(nil)
var _ ports.Session = (*Session)(nil)
