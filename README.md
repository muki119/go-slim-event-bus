# go-slim-event-bus

A strongly opinionated Redis streams abstraction mainly designed for simple inter-service communication. The purpose of this module is for it to run alongside a service's HTTP server within the same binary.

## Features 

- Concurrent Message Processing.
- Pending message housekeeping.
  - In the instance listen, the program will process a certain number of pending messages before taking new incoming messages.
- Graceful Shutdown.
- Timeout-managed handlers to prevent processes from running indefinitely.
- Exponential backoff for retrying failed Redis reads and pending-message claims.
- OpenTelemetry tracing for message publishing and handler execution.

# Installation

In terminal , with your Go project as the current directory paste the following :

``` bash
go get github.com/muki119/go-slim-event-bus/v2
```

# Usage

## Create Event Bus instance from config

``` Go
ebConfig := &eventbus.EventBusConfig{
    ConnectionConfig: conn,
    ConsumerName:  "ConsumerFizz",
    ConsumerGroup: "FizzGroup",
    MaxCount:      100,
    Timeout:       3 * time.Second,
    MaxConcurrent: int64(runtime.NumCPU() * 10),
    Tracer:        tracer,
    MinDelay:      100 * time.Millisecond,
    MaxDelay:      10 * time.Second,
    Multiplier:    2.0,
    MaxRetries:    5,
}
eventBus := ebConfig.NewFromConfig()
```

## Create Event Bus instance from the constructor

```Go
eventBus := eventbus.NewStreamsEventBus(
    "ConsumerFizz",
    "FizzGroup",
    conn,
    100,
    3*time.Second,
    int64(runtime.NumCPU() * 10),
    tracer,
    100*time.Millisecond,
    10*time.Second,
    2.0,
    5,
)
```

## Register a Stream to listen to and a handler for its incoming data

Registration of a stream and handler function should be made before.

```Go
func HandleUserCreation(ctx context.Context, data map[string]interface{}) error {
    return nil
}

eventBus.StreamHandler("user.created", HandleUserCreation)
```

Handlers receive a timeout-managed context and return an error when processing
fails. The event bus acknowledges a message only after its handler completes
successfully.

## Handle errors

Register an optional error handler to receive Redis errors, handler errors, and
message acknowledgement errors:

```Go
eventBus.ErrorHandler(func(ctx context.Context, err error, data map[string]interface{}) {
    log.Printf("event bus error: %v", err)
})
```

## To Listen

The listen method returns a channel that will only return errors and no other value.
This is to ensure the program is non-blocking

``` Go
err := <-eventBus.Listen() 
```

## To send a message

State the context, stream name, and a map containing the message data.

``` Go
ctx := context.Background()
message := map[string]interface{}{
    "user_id":   "1a2b3c",
    "user_name": "John Doe",
}
if err := eventBus.Send(ctx, "user.created", message); err != nil {
    log.Printf("Error occurred while sending message: %v", err)
}
```

`Send` injects the globally configured OpenTelemetry propagation fields
(`traceparent` and `tracestate`, when available) into the Redis stream entry.
The listener extracts those fields and creates the handler span as a child of
the publishing trace.

## To close the instance.

Waits for all messages acquired before closure to be processed or timeout, then closes the listener and connection.

``` Go
err := eventBus.Close() 
```


## Configuration options

|Field|Description|
|-|-|
|Connection|Pointer to the Redis connection instance.|
|ConsumerName|Name of the consumer instance. Used by the client to identify itself within the consumer groups.|
|ConsumerGroup|Name of consumer group to be attached to for each stream.|
|MaxCount|Maximum number of messages in each stream every read.|
|Timeout|Max amount of time for handlers to process a message.|
|MaxConcurrent|Maximum amount of messages that can be concurrently handled.|
|Tracer|OpenTelemetry tracer used for handler spans. Defaults to a no-op tracer when nil.|
|MinDelay|Initial delay for Redis retries. Defaults to 100 milliseconds when zero or negative.|
|MaxDelay|Maximum delay for Redis retries. Defaults to 10 seconds when zero or negative.|
|Multiplier|Multiplier used by exponential retry backoff. Defaults to 2.0 when less than 1.|
|MaxRetries|Maximum number of retries for a failing Redis read or pending-message claim. `0` retries forever.|

## Examples

### From Config

``` Go
ebConfig := &eventbus.EventBusConfig{
    ConnectionConfig: conn,
    ConsumerName:  "ConsumerFizz",
    ConsumerGroup: "FizzGroup",
    MaxCount:      100,
    Timeout:       3 * time.Second,
    MaxConcurrent: int64(runtime.NumCPU() * 10),
    Tracer:        tracer,
    MinDelay:      100 * time.Millisecond,
    MaxDelay:      10 * time.Second,
    Multiplier:    2.0,
    MaxRetries:    5,
}
eventBus := ebConfig.NewFromConfig()

shutdownChan := make(chan struct{}, 1)
go func() {
    exitSignal := make(chan os.Signal, 1)
    signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
    <-exitSignal
    if err := eventBus.Close(); err != nil {
        fmt.Printf("Error occurred while closing event bus: %v", err)
    }
    close(shutdownChan)
}()

eventBus.StreamHandler("user.created", HandleUserCreation)
eventBus.StreamHandler("user.deleted", HandleUserDeletion)

if err := <-eventBus.Listen(); err != nil {
    fmt.Printf("Error occurred: %v", err)
    if err := eventBus.Close(); err != nil {
        fmt.Printf("Error occurred while closing event bus: %v", err)
    }
    close(shutdownChan)
}
<-shutdownChan
```

### From Constructor

```Go
eventBus := eventbus.NewStreamsEventBus(
    "ConsumerFizz",
    "FizzGroup",
    conn,
    100,
    3*time.Second,
    int64(runtime.NumCPU() * 10),
    tracer,
    100*time.Millisecond,
    10*time.Second,
    2.0,
    5,
)

shutdownChan := make(chan struct{}, 1)
go func() {
    exitSignal := make(chan os.Signal, 1)
    signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
    <-exitSignal
    if err := eventBus.Close(); err != nil {
        fmt.Printf("Error occurred while closing event bus: %v", err)
    }
    close(shutdownChan)
}()

eventBus.StreamHandler("user.created", HandleUserCreation)
eventBus.StreamHandler("user.deleted", HandleUserDeletion)

if err := <-eventBus.Listen(); err != nil {
    fmt.Printf("Error occurred: %v", err)
    if err := eventBus.Close(); err != nil {
        fmt.Printf("Error occurred while closing event bus: %v", err)
    }
    close(shutdownChan)
}
<-shutdownChan
```

## Recommendations

- Keep handlers aware of the timeout context passed to them and stop work when
  `ctx.Done()` is closed.
- Register all stream handlers before calling `Listen`.
- For I/O-bound handlers, a starting point such as
  `int64(runtime.NumCPU() * 10)` can keep CPU cores busy while handlers wait
  on external services. Use a lower value for CPU-bound handlers and tune it
  based on workload and downstream capacity.
- Use `MaxRetries` together with `MinDelay`, `MaxDelay`, and `Multiplier` to
  control recovery from temporary Redis failures. Successful reads reset the
  backoff.

## Future Additions/Improvments

- Dead letter queueing
