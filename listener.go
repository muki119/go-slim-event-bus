package seb

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// performs the actual handling of the event creates and manages the timeout context.
func (eventBus *StreamsEventBus) handleFunc(f Handler, data any) error {
	timeoutCtx, cancel := context.WithTimeout(eventBus.ctx, eventBus.Timeout)
	errChan := make(chan error) // error channel
	defer cancel()
	go func() {
		errChan <- f(timeoutCtx, data)
	}()
	select {
	case err := <-errChan: // if the function returns before the timeout
		if err != nil {
			return err
		}
	case <-timeoutCtx.Done(): // if the timeout is done before the function returns
		return timeoutCtx.Err() // return a timeout error
	}
	return nil
}

func (eventBus *StreamsEventBus) processMessages(stream string, messages []redis.XMessage) { // blocking
	for _, message := range messages { // iterates through consumers incoming messages
		if err := eventBus.maxConcurrentSem.Acquire(eventBus.ctx, 1); err != nil {
			slog.Error(err.Error())
			continue
		}
		go func() {
			defer eventBus.maxConcurrentSem.Release(1)
			err := eventBus.handleFunc(eventBus.streamTable[stream], message.Values) // creates another go routine
			if err != nil {                                                          // if there's an error processing
				slog.Error(err.Error())
				return
			}
			_, err = eventBus.Connection.XAck(eventBus.ctx, stream, eventBus.ConsumerGroup, message.ID).Result()
			if err != nil {
				slog.Error("Error while acknowledging event", err)
			}
		}() // processes message according to stream it comes from.
	}
}

// performs "House Cleaning" such as removal of pending before officially starting and listening for new streams messages
func (eventBus *StreamsEventBus) processPendingMessages() error {
	for stream := range eventBus.streamTable {
		messages, _, err := eventBus.Connection.XAutoClaim(eventBus.ctx, &redis.XAutoClaimArgs{ // claims about 100 claims from any
			Stream:   stream,
			Count:    eventBus.MaxCount,
			MinIdle:  0,
			Consumer: eventBus.ConsumerName,
			Start:    "0-0",
			Group:    eventBus.ConsumerGroup,
		}).Result()
		if err != nil {
			return err
		}
		eventBus.processMessages(stream, messages)
	}

	return nil
}

// Listens for incoming messages from stream for their associated consumer groups
//
// O(n*m) operation where n is the amount of streams and m is the maximum amount of messages in each stream.
func (eventBus *StreamsEventBus) listen() {
	StreamsArr := make([]string, len(eventBus.streamTable)*2)
	for stream := range eventBus.streamTable {
		StreamsArr = append(StreamsArr, stream, ">")
	}

	eventBus.Listening.Store(true)
	for eventBus.Listening.Load() { // while listening
		incomingStreams, err := eventBus.Connection.XReadGroup(eventBus.ctx, // Listen for incoming streams
			&redis.XReadGroupArgs{
				Streams:  StreamsArr,
				Group:    eventBus.ConsumerGroup,
				Consumer: eventBus.ConsumerName,
				Count:    eventBus.MaxCount,
				Block:    2 * time.Second, // to prevent indefinite blocking
			}).Result()

		if err != nil {
			slog.Error(err.Error())
			continue
		}

		for _, stream := range incomingStreams { //for every stream in the stream batch
			messageOperation := eventBus.streamTable[stream.Stream]
			if messageOperation == nil { // if there is no operation for the stream
				slog.Error(fmt.Sprintf("Stream Opperation for %s doesn't exist", stream.Stream))
				continue
			}
			eventBus.processMessages(stream.Stream, stream.Messages) // blocking
		}

	}

}

func (eventBus *StreamsEventBus) Listen() chan error {
	eventBus.waitGroup.Add(1) // wait group for graceful close
	errChan := make(chan error)
	go func() {
		defer func() {
			eventBus.waitGroup.Done()
			close(errChan)
		}()
		if err := eventBus.initialize(); err != nil {
			errChan <- err
			return
		}
		eventBus.listen()
	}()
	return errChan // returning an error channel because function needs to be concurrent
}
