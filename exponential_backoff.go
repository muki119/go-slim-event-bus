package eventbus

import (
	"math/rand"
	"time"
)

type exponentialBackoff struct {
	minDelay     time.Duration // Minimum delay
	maxDelay     time.Duration // Maximum delay
	multiplier   float64       // Multiplier for the backoff
	currentDelay time.Duration // Current delay
	retryCount   int           // Number of retries
}

const (
	defaultMinDelay   = 100 * time.Millisecond
	defaultMaxDelay   = 10 * time.Second
	defaultMultiplier = 2.0
)

func (b *exponentialBackoff) GetRetryCount() int {
	return b.retryCount
}

func (b *exponentialBackoff) NextDelay() time.Duration {
	if b.currentDelay == 0 {
		b.currentDelay = b.minDelay
	} else {
		newDelay := float64(b.currentDelay) * b.multiplier
		// set the current delay to the minimum of the calculated delay and the maximum delay - preventing it from exceeding the maximum delay
		b.currentDelay = time.Duration(min(newDelay, float64(b.maxDelay)))

	}
	b.retryCount++ // increment the retry count for each call to NextDelay

	jitter := time.Duration(float64(b.currentDelay) * 0.1)              // 10% of the current delay
	return b.currentDelay + time.Duration(rand.Int63n(int64(jitter)+1)) // add a random ammount from 0 to 10% of the current delay to the current delay
}

func (b *exponentialBackoff) Reset() {
	b.currentDelay = 0
	b.retryCount = 0

}

// NewExponentialBackoff creates a new ExponentialBackoff instance.
// minDelay is the minimum delay; default is 100 milliseconds.
// maxDelay is the maximum delay; default is 10 seconds.
// multiplier is the multiplier for the backoff; default is 2.0.
func newExponentialBackoff(minDelay, maxDelay time.Duration, multiplier float64) *exponentialBackoff {
	if minDelay <= 0 {
		minDelay = defaultMinDelay
	}
	if maxDelay <= 0 {
		maxDelay = defaultMaxDelay
	}

	if multiplier < 1 {
		multiplier = defaultMultiplier
	}
	maxDelay = max(minDelay, maxDelay) // Ensure maxDelay is not less than minDelay
	return &exponentialBackoff{
		minDelay:     minDelay,
		maxDelay:     maxDelay,
		multiplier:   multiplier,
		currentDelay: 0,
	}
}
