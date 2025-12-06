package stream

import "errors"

var (
	ErrInvalidParams     = errors.New("invalid stream parameters")
	ErrStreamInProgress  = errors.New("stream already in progress")
	ErrStreamStartFailed = errors.New("stream failed to start")
)
