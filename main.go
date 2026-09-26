package eventbus

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Handler is a function that handles messages from a stream.
type Handler func(context.Context, map[string]interface{}) error
type ErrorHandler func(context.Context, error, map[string]interface{})

// StreamsEventBus Heavily Opinionated Redis Stream Manager , meant to act as a layer of abstraction from the redis stream
// hopefully allowing for easier management of the stream
type StreamsEventBus struct {
	ListenerConnection *redis.Client // Redis client connection -- is only one connection
	SenderConnection   *redis.Client // A pooled connection to allow concurrent message
	AckConnection      *redis.Client // A separate connection for acknowledging messages - because the listener connection is blocking and shouldnt be used for acknowledgments.
	ConsumerGroup      string        // The name of the consumer group to be attached to for each stream
	ConsumerName       string        // Name of the consumer within the consumer group
	streamTable        map[string]Handler
	errorHandler       ErrorHandler
	Listening          atomic.Bool // Indicates whether the event bus is currently listening for events.

	MaxCount  int64           // Maximum Messages per Stream within a Read
	Timeout   time.Duration   // Timeout stores the maximum duration a message can be processed before timing out.
	waitGroup *sync.WaitGroup // wait group for Listen process - This is used for graceful closure.
	ctx       context.Context // Context for the event bus

	Tracer trace.Tracer // Tracer used to wrap each handler execution in a span. Defaults to a no-op tracer if not provided.

	maxConcurrentSem *semaphore.Weighted // Semaphore for limiting the max goroutines that can be spawned for processing tasks.
	maxConcurrent    int64
}
type EventBusConfig struct {
	ConnectionConfig *redis.Options // Redis client connection configuration
	ConsumerName     string         // Name of the consumer within the consumer group
	ConsumerGroup    string         // The name of the consumer group to be attached to for each stream
	MaxCount         int64          // Maximum Messages per Stream within a Read
	Timeout          time.Duration  // Timeout stores the maximum duration a message can be processed before timing out.
	MaxConcurrent    int64          // The max goroutines that can be spawned for processing tasks.
	Tracer           trace.Tracer   // Optional. Defaults to a no-op tracer if nil.
}

func (config *EventBusConfig) NewFromConfig() *StreamsEventBus {
	return NewStreamsEventBus(config.ConsumerName, config.ConsumerGroup, config.ConnectionConfig, config.MaxCount, config.Timeout, config.MaxConcurrent, config.Tracer)
}

// NewStreamsEventBus The consumer group will be the same for all streams.
func NewStreamsEventBus(consumerName string, consumerGroup string, options *redis.Options, maxCount int64, timeout time.Duration, maxConcurrent int64, tracer trace.Tracer) *StreamsEventBus {
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("eventbus")
	}
	listenerConnectionOptions := *options  // deReferenced copy of options
	listenerConnectionOptions.PoolSize = 1 // has to be once since we only need one listener
	listenerConnectionOptions.MinIdleConns = 1

	ackConnectionOptions := *options
	ackConnectionOptions.PoolSize = int(maxConcurrent) // set the pool size for the acknowledgment connection to match the max concurrent processing limit
	senderConnectionOptions := *options

	newStreamEventBus := &StreamsEventBus{
		ListenerConnection: redis.NewClient(&listenerConnectionOptions),
		AckConnection:      redis.NewClient(&ackConnectionOptions), // this is for acknowledging messages
		SenderConnection:   redis.NewClient(&senderConnectionOptions),

		ConsumerGroup:    consumerGroup,
		ConsumerName:     consumerName,
		streamTable:      make(map[string]Handler),
		MaxCount:         maxCount,
		Timeout:          timeout,
		Tracer:           tracer,
		waitGroup:        &sync.WaitGroup{},
		ctx:              context.Background(),
		maxConcurrentSem: semaphore.NewWeighted(maxConcurrent),
		maxConcurrent:    maxConcurrent,
	}
	newStreamEventBus.Listening.Store(false)
	return newStreamEventBus

}

// Handler Registers a handlerFunc for a stream s
//
//	All handler functions should be declared before init and listening
func (eventBus *StreamsEventBus) StreamHandler(stream string, handlerFunc Handler) {
	if _, exists := eventBus.streamTable[stream]; exists { // if it already exists
		// then panic and dont allow it to run because it shouldnt be allowed
		panic(fmt.Sprintf("%s already has a handler", stream))
	}
	eventBus.streamTable[stream] = handlerFunc
}

func (eventBus *StreamsEventBus) ErrorHandler(errorFunc ErrorHandler) {
	eventBus.errorHandler = errorFunc
}

func (eventBus *StreamsEventBus) Close() error {
	eventBus.Listening.Store(false)
	timedCtx, cancel := context.WithTimeout(eventBus.ctx, eventBus.Timeout)
	defer cancel()
	err := eventBus.maxConcurrentSem.Acquire(timedCtx, eventBus.MaxCount)
	if err != nil {
		return err
	}
	eventBus.waitGroup.Wait()

	var closeErr error

	err = eventBus.ListenerConnection.Close()

	if err != nil {
		closeErr = err
	}

	err = eventBus.SenderConnection.Close()
	if err != nil {
		closeErr = err
	}

	return closeErr
}
