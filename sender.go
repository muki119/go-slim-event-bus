package eventbus

import (
	"context"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Send publishes message to stream, injecting the trace context from ctx (traceparent/tracestate)
// as regular fields on the message - Redis Streams entries are flat, so propagation headers ride
// along in the same map as the caller's own data, using whatever propagator is globally registered
// via otel.SetTextMapPropagator (a no-op if the host service never set one).
func (eventBus *StreamsEventBus) Send(ctx context.Context, stream string, message map[string]interface{}) error {
	carrier := make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(carrier))
	for key, value := range carrier {
		message[key] = value
	}

	_, err := eventBus.SenderConnection.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: message,
		ID:     "*",
	}).Result() // will return the string of the new entry (not currently needed ) or an error.
	if err != nil {
		return err
	}
	return nil
}
