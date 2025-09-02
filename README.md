# go-slim-event-bus

A strongly opinionated Redis streams abstraction mainly designed for simple inter-service communication. The purpose of this module is for it to run alongside a service's HTTP server within the same binary.

## Features 
- Concurrent Message Processing.
- Pending message housekeeping.
    - In the instance listen, the program will process a certain number of pending messages before taking new incoming messages.
- Graceful Shutdown.
- Timeout managed handlers to prevent processes from running indefinitely .

# Installation 
In terminal , with your Go project as the current directory paste the following :
``` bash
go get github.com/muki119/go-slim-event-bus
```

# Usage
## Create Event Bus instance from config.
``` Go
ebConfig := &seb.EventBusConfig{
    Connection:    conn,
    ConsumerName:  "ConsumerFizz",
    ConsumerGroup: "FizzGroup",
    MaxCount:      100,
    Timeout:       3 * time.Second,
    MaxConcurrent: int64(runtime.NumCPU() / 10),
}
eventBus := ebConfig.NewFromConfig()
```

## Create Event Bus instance from the constructor.
```Go
eventBus := seb.NewStreamsEventBus("ConsumerFizz", "FizzGroup", conn, 100, 3*time.Second, int64(runtime.NumCPU()/10))
```

## Register a Stream to listen to and a handler for its incoming data.
Registration of a stream and handler function should be made before.
```Go 
eventBus.StreamHandler("user.created", HandleUserCreation)
```

## To Listen 
The listen method returns a channel that will only return errors and no other value.   
This is to ensure the program is non-blocking
``` Go
err := <-eventBus.Listen() 
```

## To send a message
Simply state the stream name and a map containing the message data.
``` Go
message := map[string]interface{}{
    "user_id":   "1a2b3c",
    "user_name": "John Doe",
}
eventBus.Send("user.created", message) 
```

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

## Examples
### From Config
``` Go
ebConfig := &seb.EventBusConfig{
    Connection:    conn,
    ConsumerName:  "ConsumerFizz",
    ConsumerGroup: "FizzGroup",
    MaxCount:      100,
    Timeout:       3 * time.Second,
    MaxConcurrent: int64(runtime.NumCPU() / 10),
}
eventBus := ebConfig.NewFromConfig()

shutdownChan := make(chan struct{}, 1)
go func() {
    exitSignal := make(chan os.Signal)
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
eventBus := seb.NewStreamsEventBus("ConsumerFizz", "FizzGroup", conn, 100, 3*time.Second, int64(runtime.NumCPU()/10))

shutdownChan := make(chan struct{}, 1)
go func() {
    exitSignal := make(chan os.Signal)
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
- For handler functions , keep them aware of timeout context passed in each handler.


## Future Additions/Improvments
 - Dead letter queueing 

