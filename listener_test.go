package eventbus

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace/noop"
)

func getEnvString(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	value := getEnvString(key, "")
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}

// waitForListening blocks until bus reports Listening, errChan reports a startup
// failure, or timeout elapses - whichever happens first. Polls on a short tick so
// the happy path returns as soon as the bus is actually up, instead of always
// waiting out the full timeout.
func waitForListening(t *testing.T, name string, bus *StreamsEventBus, errChan <-chan error, timeout time.Duration) {
	t.Helper()

	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-errChan:
			if err != nil {
				t.Fatalf("%s failed to start: %v", name, err)
			}
		case <-ticker.C:
			if bus.Listening.Load() {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s to start", name)
		}
	}
}

func generateRandomString(length int) string {
	b := make([]byte, length) // makes a byte slice of the specified length
	_, err := rand.Read(b)
	if err != nil {
		panic(err) // handle error appropriately in production code
	}
	return fmt.Sprintf("%x", b)[:length] // convert to hex string and truncate to desired length
}

func TestListener(t *testing.T) {
	// basically
	// create a listener and a sender , send a message from the sender and check if the listener recieves it and processes it
	// the handler should send to a channel which should hold the message and be blocked until the messgae is recoeved or the test handler times out
	testChannel := make(chan map[string]interface{}, 1)
	var testHandler = func(ctx context.Context, message map[string]interface{}) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case testChannel <- message:
			return nil
		}
	}

	testErrorHandlerChannel := make(chan error, 1)

	var testErrorHandler = func(ctx context.Context, err error, message map[string]interface{}) {
		select {
		case <-ctx.Done():
			return
		case testErrorHandlerChannel <- err:
			return
		}
	}
	go func(t *testing.T) {
		for err := range testErrorHandlerChannel {
			t.Errorf("Error handler received error: %v", err)
		}
	}(t)

	var testRedisOptions = &redis.Options{
		Addr:     fmt.Sprintf("%s:%s", getEnvString("TEST_REDIS_HOST", "localhost"), getEnvString("TEST_REDIS_PORT", "6379")),
		Password: getEnvString("TEST_REDIS_PASSWORD", ""),
		DB:       getEnvInt("TEST_REDIS_DB", 0),
	}

	var testListener = NewStreamsEventBus(
		"test-listener",
		"test-listener-consumer-group",
		testRedisOptions,
		1,
		5*time.Second,
		1,
		noop.NewTracerProvider().Tracer("test"),
		defaultMinDelay,
		defaultMaxDelay,
		defaultMultiplier,
		5,
	)

	entropy := generateRandomString(16)
	testStreamName := fmt.Sprintf("test-stream-%s", entropy)

	t.Logf("Using stream name: %s", testStreamName)
	testListener.StreamHandler(testStreamName, testHandler)
	testListener.ErrorHandler(testErrorHandler)

	var testSender = NewStreamsEventBus(
		"test-sender",
		"test-sender-consumer-group",
		testRedisOptions,
		1,
		5*time.Second,
		1,
		noop.NewTracerProvider().Tracer("test"),
		defaultMinDelay,
		defaultMaxDelay,
		defaultMultiplier,
		5,
	)

	t.Cleanup(
		func() {

			testListener.Close()
			testSender.Close()

			cleanupConn := redis.NewClient(testRedisOptions)
			defer cleanupConn.Close()
			if err := cleanupConn.Del(context.Background(), testStreamName).Err(); err != nil {
				t.Logf("failed to delete test stream %q: %v", testStreamName, err)
			}
		},
	)

	listenerErr := testListener.Listen()
	senderErr := testSender.Listen()

	started := t.Run("listener and sender start", func(t *testing.T) {
		waitForListening(t, "listener", testListener, listenerErr, 3*time.Second)
		waitForListening(t, "sender", testSender, senderErr, 3*time.Second)
	})
	if !started {
		t.Fatal("listener/sender did not start; aborting remaining subtests")
	}

	t.Run("sends message without error", func(t *testing.T) {
		testMessage := map[string]interface{}{
			"val": "test-value",
		}
		err := testSender.Send(context.Background(), testStreamName, testMessage)
		if err != nil {
			t.Errorf("Error sending message: %v", err)
		}
	})
	t.Run("sent message is received by listener", func(t *testing.T) {
		select {
		case receivedMessage := <-testChannel:
			t.Logf("Received message: %v\n", receivedMessage)
			if receivedMessage["val"] != "test-value" {
				t.Errorf("Expected message value 'test-value', got '%v'", receivedMessage["val"])
			}
			return
		case <-time.After(5 * time.Second):
			t.Error("Timeout waiting for message")
		}

	})
}
