package internal

import (
	"context"
)

type Runtime struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	streams *streamRouter
}

func newRuntime(parentCtx context.Context) *Runtime {
	ctx, cancel := context.WithCancel(parentCtx)
	return &Runtime{
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		streams: newStreamRoute(),
	}
}

func (r *Runtime) stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *Runtime) wait() <-chan struct{} {
	return r.done
}
