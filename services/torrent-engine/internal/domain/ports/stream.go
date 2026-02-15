package ports

import (
	"context"
	"io"
)

type StreamReader interface {
	io.ReadSeekCloser
	SetContext(context.Context)
	SetReadahead(int64)
	SetResponsive()
}
