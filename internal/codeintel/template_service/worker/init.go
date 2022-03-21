package worker

import (
	"context"

	"github.com/sourcegraph/sourcegraph/cmd/worker/shared/job"
	"github.com/sourcegraph/sourcegraph/internal/env"
	"github.com/sourcegraph/sourcegraph/internal/goroutine"
)

type WorkerJob struct{}

func NewWorkerJob() job.Job {
	return &WorkerJob{}
}

func (j *WorkerJob) Config() []env.Config {
	return []env.Config{
		todoRoutineConfigInst,
	}
}

func (j *WorkerJob) Routines(ctx context.Context) ([]goroutine.BackgroundRoutine, error) {
	return []goroutine.BackgroundRoutine{
		NewTodoRoutine(),
	}, nil
}
