package v1

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/redis/go-redis/v9"
)

// Handler is a function that handles messages from a stream.
type Handler func(context.Context, any) error

// StreamsEventBus Heavily Opinionated Redis Stream Manager , meant to act as a layer of abstraction from the redis stream
// hopefully allowing for easier management of the stream
type StreamsEventBus struct {
	Connection    *redis.Client // Redis client connection
	ConsumerGroup string        // The name of the consumer group to be attached to for each stream
	ConsumerName  string        // Name of the consumer within the consumer group
	streamTable   map[string]Handler
	Listening     atomic.Bool // Indicates whether the event bus is currently listening for events.

	MaxCount  int64           // Maximum Messages per Stream within a Read
	Timeout   time.Duration   // Timeout stores the maximum duration a message can be processed before timing out.
	waitGroup *sync.WaitGroup // wait group for Listen process - This is used for graceful closure.
	ctx       context.Context // Context for the event bus

	maxConcurrentSem *semaphore.Weighted // Semaphore for limiting the max goroutines that can be spawned for processing tasks.
}
type EventBusConfig struct {
	Connection    *redis.Client // Redis client connection
	ConsumerName  string        // Name of the consumer within the consumer group
	ConsumerGroup string        // The name of the consumer group to be attached to for each stream
	MaxCount      int64         // Maximum Messages per Stream within a Read
	Timeout       time.Duration // Timeout stores the maximum duration a message can be processed before timing out.
	MaxConcurrent int64         // The max goroutines that can be spawned for processing tasks.
}

func (config *EventBusConfig) NewFromConfig() *StreamsEventBus {
	return NewStreamsEventBus(config.ConsumerName, config.ConsumerGroup, config.Connection, config.MaxCount, config.Timeout, config.MaxConcurrent)
}

// NewStreamsEventBus The consumer group will be the same for all streams.
func NewStreamsEventBus(consumerName string, consumerGroup string, conn *redis.Client, maxCount int64, timeout time.Duration, maxConcurrent int64) *StreamsEventBus {
	newStreamEventBus := &StreamsEventBus{
		Connection:       conn,
		ConsumerGroup:    consumerGroup,
		ConsumerName:     consumerName,
		streamTable:      make(map[string]Handler),
		MaxCount:         maxCount,
		Timeout:          timeout,
		waitGroup:        &sync.WaitGroup{},
		ctx:              context.Background(),
		maxConcurrentSem: semaphore.NewWeighted(maxConcurrent),
	}
	newStreamEventBus.Listening.Store(false)
	return newStreamEventBus

}

// Handler Registers a handlerFunc for a stream s
//
//	All handler functions should be declared before init and listening
func (eventBus *StreamsEventBus) StreamHandler(stream string, handlerFunc Handler) {
	eventBus.streamTable[stream] = handlerFunc
}

// Initialize Joins all streams and consumer groups , and removes some pending messages in all streams associated under the consumer group
func (eventBus *StreamsEventBus) initialize() error {
	for stream := range eventBus.streamTable { // create all the groups for all the streams
		_, err := eventBus.Connection.XGroupCreateMkStream(eventBus.ctx, stream, eventBus.ConsumerGroup, "$").Result()
		if err != nil && err.Error() != "BUSYGROUP" {
			return err
		}
	}
	err := eventBus.processPendingMessages()
	if err != nil {
		slog.Error("Error while processing pending events", err)
		return err
	}
	return nil
}

func (eventBus *StreamsEventBus) Close() error {
	eventBus.Listening.Store(false)
	eventBus.waitGroup.Wait()
	err := eventBus.Connection.Close()
	if err != nil {
		return err
	}
	return nil
}
