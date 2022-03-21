package worker

import (
	"context"

	"github.com/sourcegraph/sourcegraph/internal/goroutine"
)

func NewTodoRoutine() goroutine.BackgroundRoutine {
	routine := &routine{}

	return goroutine.NewPeriodicGoroutine(context.Background(), todoRoutineConfigInst.Interval, routine)
}
