package eventbus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

func isConsumerGroupAlreadyExists(err error) bool {
	if err == nil {
		return false
	}

	errText := err.Error()
	return errText == "BUSYGROUP" || strings.HasPrefix(errText, "BUSYGROUP ")
}

//	whether err is a network-level timeout (the client gave up waiting
//
// for a response within its ReadTimeout) rather than a real failure.
func isIdleReadTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// performs the actual handling of the event, manages the timeout context, and wraps the
// handler execution in a span so both the handler's own spans and any error passed to the
// ErrorHandler nest under the same trace. If the message carries a traceparent/tracestate
// (injected by Send on the publishing side), the span is a child of that remote trace instead
// of starting a new one.
func (eventBus *StreamsEventBus) executeHandlerFunction(stream string, f Handler, data map[string]interface{}) (context.Context, error) {
	carrier := make(map[string]string, 2)
	if v, ok := data["traceparent"].(string); ok {
		carrier["traceparent"] = v
	}
	if v, ok := data["tracestate"].(string); ok {
		carrier["tracestate"] = v
	}
	parentCtx := otel.GetTextMapPropagator().Extract(eventBus.ctx, propagation.MapCarrier(carrier))

	timeoutCtx, cancel := context.WithTimeout(parentCtx, eventBus.Timeout)
	defer cancel()

	ctx, span := eventBus.Tracer.Start(timeoutCtx, fmt.Sprintf("eventbus.handle %s", stream))
	defer span.End()

	errChan := make(chan error) // error channel
	go func() {
		errChan <- f(ctx, data)
	}()
	select {
	case err := <-errChan: // if the function returns before the timeout
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return ctx, err
		}
	case <-ctx.Done(): // if the timeout is done before the function returns
		err := ctx.Err()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return ctx, err // return a timeout error
	}
	return ctx, nil
}

func (eventBus *StreamsEventBus) processMessages(stream string, messages []redis.XMessage) { // blocking
	for _, message := range messages { // iterates through consumers incoming messages
		if err := eventBus.maxConcurrentSem.Acquire(eventBus.ctx, 1); err != nil {
			eventBus.errorHandler(eventBus.ctx, fmt.Errorf("failed to acquire semaphore: %w", err), message.Values)
			continue
		}
		go func() {
			defer eventBus.maxConcurrentSem.Release(1)
			funcCtx, err := eventBus.executeHandlerFunction(stream, eventBus.streamTable[stream], message.Values) // creates another go routine
			if err != nil {                                                                                       // if there's an error processing
				if eventBus.errorHandler != nil {
					eventBus.errorHandler(funcCtx, err, message.Values)
				}
				return
			}
			_, err = eventBus.AckConnection.XAck(eventBus.ctx, stream, eventBus.ConsumerGroup, message.ID).Result()
			if err != nil {
				if eventBus.errorHandler != nil {
					eventBus.errorHandler(funcCtx, err, message.Values)
				}
			}
		}() // processes message according to stream it comes from.
	}
}

// performs "House Cleaning" such as removal of pending before officially starting and listening for new streams messages
func (eventBus *StreamsEventBus) processPendingMessages() error {
	for stream := range eventBus.streamTable {
		for {
			messages, _, err := eventBus.ListenerConnection.XAutoClaim(eventBus.ctx, &redis.XAutoClaimArgs{ // claims about 100 claims from any
				Stream:   stream,
				Count:    eventBus.MaxCount,
				MinIdle:  0,
				Consumer: eventBus.ConsumerName,
				Start:    "0-0",
				Group:    eventBus.ConsumerGroup,
			}).Result()
			if err != nil {
				if errors.Is(err, redis.Nil) { // no pending messages for this stream - not an error
					break
				}
				if isIdleReadTimeout(err) { // the idle read simply timed out - not an error
					continue
				}
				if eventBus.errorHandler != nil {
					eventBus.errorHandler(eventBus.ctx, err, nil)
				}
				continue
			}

			eventBus.processMessages(stream, messages)
			break
		}
	}

	return nil
}

// Listens for incoming messages from stream for their associated consumer groups
//
// O(n*m) operation where n is the amount of streams and m is the maximum amount of messages in each stream.
func (eventBus *StreamsEventBus) listen() {

	StreamsArr := make([]string, 0, len(eventBus.streamTable)*2)
	for stream := range eventBus.streamTable {
		StreamsArr = append(StreamsArr, stream)
	}
	for range eventBus.streamTable {
		StreamsArr = append(StreamsArr, ">")
	}

	eventBus.Listening.Store(true)
	for eventBus.Listening.Load() { // while listening
		incomingStreams, err := eventBus.ListenerConnection.XReadGroup(eventBus.ctx, // Listen for incoming streams
			&redis.XReadGroupArgs{
				Streams:  StreamsArr,
				Group:    eventBus.ConsumerGroup,
				Consumer: eventBus.ConsumerName,
				Count:    eventBus.MaxCount,
				Block:    2 * time.Second, // to prevent indefinite blocking
			}).Result()

		if err != nil {
			if errors.Is(err, redis.Nil) || isIdleReadTimeout(err) { // no new messages, or the idle read simply timed out - not an error
				continue
			}
			if eventBus.errorHandler != nil {
				eventBus.errorHandler(eventBus.ctx, err, nil)
			}
			continue
		}

		for _, stream := range incomingStreams { //for every stream in the stream batch
			messageOperation := eventBus.streamTable[stream.Stream]
			if messageOperation == nil { // if there is no operation for the stream
				if eventBus.errorHandler != nil {
					eventBus.errorHandler(eventBus.ctx, fmt.Errorf("Stream Operation for %s doesn't exist", stream.Stream), nil)
				}
				continue
			}
			eventBus.processMessages(stream.Stream, stream.Messages) // blocking
		}

	}

}

// Initialize Joins all streams and consumer groups , and removes some pending messages in all streams associated under the consumer group
func (eventBus *StreamsEventBus) initialize() error {
	for stream := range eventBus.streamTable { // create all the groups for all the streams
		_, err := eventBus.ListenerConnection.XGroupCreateMkStream(eventBus.ctx, stream, eventBus.ConsumerGroup, "$").Result()
		if err != nil && !isConsumerGroupAlreadyExists(err) {
			return err
		}
	}
	err := eventBus.processPendingMessages()
	if err != nil {
		if eventBus.errorHandler != nil {
			eventBus.errorHandler(eventBus.ctx, err, nil)
		}
		return err
	}
	return nil
}

func (eventBus *StreamsEventBus) Listen() chan error {
	eventBus.waitGroup.Add(1) // wait group for graceful close
	errChan := make(chan error)
	go func() {
		defer func() {
			eventBus.waitGroup.Done() // close once the event bus is done listening
			close(errChan)
		}()
		if err := eventBus.initialize(); err != nil { // first process any messages that are left out
			errChan <- err
			return
		}
		eventBus.listen()
	}()
	return errChan // returning an error channel because function needs to be concurrent
}
