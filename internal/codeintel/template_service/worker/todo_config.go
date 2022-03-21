package worker

import (
	"time"

	"github.com/sourcegraph/sourcegraph/internal/env"
)

type todoRoutineConfig struct {
	env.BaseConfig

	Interval time.Duration
	// TODO
}

var todoRoutineConfigInst = &todoRoutineConfig{}

func (c *todoRoutineConfig) Load() {
	c.Interval = c.GetInterval("CODEINTEL_TODO_INTERVAL", "1s", "How frequently to run the TODO routine.")
	// TODO
}
