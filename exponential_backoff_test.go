package eventbus

import (
	"math/rand"
	"testing"
	"time"
)

func TestNewExponentialBackoff(t *testing.T) {
	t.Run("uses default values when inputs are zero or invalid", func(t *testing.T) {
		b := newExponentialBackoff(0, 0, 0)

		if got, want := b.minDelay, defaultMinDelay; got != want {
			t.Fatalf("newExponentialBackoff().minDelay = %v, want %v", got, want)
		}
		if got, want := b.maxDelay, defaultMaxDelay; got != want {
			t.Fatalf("newExponentialBackoff().maxDelay = %v, want %v", got, want)
		}
		if got, want := b.multiplier, defaultMultiplier; got != want {
			t.Fatalf("newExponentialBackoff().multiplier = %v, want %v", got, want)
		}
		if got, want := b.currentDelay, time.Duration(0); got != want {
			t.Fatalf("newExponentialBackoff().currentDelay = %v, want %v", got, want)
		}
	})

	t.Run("uses default multiplier when multiplier is below 1", func(t *testing.T) {
		b := newExponentialBackoff(50*time.Millisecond, 1*time.Second, 0.5)

		if got, want := b.multiplier, defaultMultiplier; got != want {
			t.Fatalf("newExponentialBackoff().multiplier = %v, want %v", got, want)
		}
	})

	t.Run("ensures max delay is not less than min delay", func(t *testing.T) {
		b := newExponentialBackoff(500*time.Millisecond, 200*time.Millisecond, 2.0)

		if got, want := b.minDelay, 500*time.Millisecond; got != want {
			t.Fatalf("newExponentialBackoff().minDelay = %v, want %v", got, want)
		}
		if got, want := b.maxDelay, 500*time.Millisecond; got != want {
			t.Fatalf("newExponentialBackoff().maxDelay = %v, want %v", got, want)
		}
	})
}

func TestExponentialBackoffNextDelay(t *testing.T) {
	t.Run("starts at the minimum delay and adds jitter", func(t *testing.T) {
		rand.Seed(1)
		b := newExponentialBackoff(200*time.Millisecond, 2*time.Second, 2.0)

		got := b.NextDelay()
		minExpected := 200 * time.Millisecond
		maxExpected := minExpected + time.Duration(float64(minExpected)*0.1)

		if gotToZero, want := b.currentDelay, minExpected; gotToZero != want {
			t.Fatalf("b.currentDelay = %v, want %v", gotToZero, want)
		}
		if got < minExpected || got > maxExpected {
			t.Fatalf("NextDelay() = %v, want a value in [%v, %v]", got, minExpected, maxExpected)
		}
	})

	t.Run("doubles each retry until it reaches max delay", func(t *testing.T) {
		b := newExponentialBackoff(100*time.Millisecond, 250*time.Millisecond, 2.0)

		first := b.NextDelay()
		if got, want := b.currentDelay, 100*time.Millisecond; got != want {
			t.Fatalf("first currentDelay = %v, want %v", got, want)
		}
		assertDelayInRange(t, first, 100*time.Millisecond, 110*time.Millisecond)

		second := b.NextDelay()
		if got, want := b.currentDelay, 200*time.Millisecond; got != want {
			t.Fatalf("second currentDelay = %v, want %v", got, want)
		}
		assertDelayInRange(t, second, 200*time.Millisecond, 220*time.Millisecond)

		third := b.NextDelay()
		if got, want := b.currentDelay, 250*time.Millisecond; got != want {
			t.Fatalf("third currentDelay = %v, want %v", got, want)
		}
		assertDelayInRange(t, third, 250*time.Millisecond, 275*time.Millisecond)
	})

	t.Run("resets to zero and starts the sequence again", func(t *testing.T) {
		b := newExponentialBackoff(150*time.Millisecond, 5*time.Second, 3.0)

		_ = b.NextDelay()
		_ = b.NextDelay()
		b.Reset()

		if got, want := b.currentDelay, time.Duration(0); got != want {
			t.Fatalf("b.currentDelay after Reset() = %v, want %v", got, want)
		}

		got := b.NextDelay()
		minExpected := 150 * time.Millisecond
		maxExpected := minExpected + time.Duration(float64(minExpected)*0.1)
		if got < minExpected || got > maxExpected {
			t.Fatalf("NextDelay() after Reset() = %v, want a value in [%v, %v]", got, minExpected, maxExpected)
		}
		if gotCurrent, want := b.currentDelay, minExpected; gotCurrent != want {
			t.Fatalf("b.currentDelay after reset cycle = %v, want %v", gotCurrent, want)
		}
	})
}

func assertDelayInRange(t *testing.T, got, min, max time.Duration) {
	t.Helper()

	if got < min || got > max {
		t.Fatalf("delay = %v, want a value in [%v, %v]", got, min, max)
	}
}
