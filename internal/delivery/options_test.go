package delivery

import (
	"testing"
	"time"
)

func TestBackoffCapsWithoutOverflow(t *testing.T) {
	o, err := (Options{RetryMin: time.Second, RetryMax: 5 * time.Second}).defaults()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		attempts int64
		want     time.Duration
	}{{1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {4, 5 * time.Second}, {1000000000, 5 * time.Second}} {
		if got := o.retryDelay(test.attempts); got != test.want {
			t.Fatalf("attempt %d: %v", test.attempts, got)
		}
	}
	for _, o := range []Options{{MaxWorkers: 33}, {RetryMin: time.Minute, RetryMax: time.Second}, {PollInterval: time.Nanosecond}, {AttemptTimeout: 25 * time.Hour}} {
		if o.Validate() == nil {
			t.Fatal("invalid options accepted")
		}
	}
}
