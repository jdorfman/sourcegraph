package template

import (
	"context"

	"github.com/sourcegraph/sourcegraph/internal/observation"
)

type Service struct {
	templateStore Store
	operations    *operations
}

func newService(templateStore Store, observationContext *observation.Context) *Service {
	return &Service{
		templateStore: templateStore,
		operations:    newOperations(observationContext),
	}
}

func (s *Service) Todo(ctx context.Context) (err error) {
	ctx, endObservation := s.operations.todo.With(ctx, &err, observation.Args{})
	defer endObservation(1, observation.Args{})

	// TODO
	return nil
}
