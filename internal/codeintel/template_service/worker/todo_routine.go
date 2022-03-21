package worker

import (
	"context"

	"github.com/sourcegraph/sourcegraph/internal/goroutine"
)

type routine struct{}

var _ goroutine.Handler = &routine{}
var _ goroutine.ErrorHandler = &routine{}

func (r *routine) Handle(ctx context.Context) error {
	// TODO
	return nil
}

func (r *routine) HandleError(err error) {
	// TODO
}
