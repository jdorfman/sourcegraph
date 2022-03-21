package template

import (
	"context"

	"github.com/sourcegraph/sourcegraph/internal/codeintel/template_service/store"
)

type Store interface {
	Todo(ctx context.Context) ([]store.Todo, error)
}
