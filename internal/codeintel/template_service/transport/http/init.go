package http

import (
	"sync"

	"github.com/inconshreveable/log15"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"

	template "github.com/sourcegraph/sourcegraph/internal/codeintel/template_service"
	"github.com/sourcegraph/sourcegraph/internal/observation"
	"github.com/sourcegraph/sourcegraph/internal/trace"
)

var (
	handler     *Handler
	handlerOnce sync.Once
)

func GetHandler(svc *template.Service) *Handler {
	handlerOnce.Do(func() {
		observationContext := &observation.Context{
			Logger:     log15.Root(),
			Tracer:     &trace.Tracer{Tracer: opentracing.GlobalTracer()},
			Registerer: prometheus.DefaultRegisterer,
		}

		handler = newHandler(svc, observationContext)
	})

	return handler
}
