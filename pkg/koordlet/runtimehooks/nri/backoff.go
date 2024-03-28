package nri

import "time"

type Backoff struct {
	initInterval time.Duration
	maxInterval  time.Duration
	multiplier   float64
}

func NewBackOff(initInterval, maxInterval time.Duration, mul float64) Backoff {
	return Backoff{
		initInterval: initInterval,
		maxInterval:  maxInterval,
		multiplier:   mul,
	}
}

func (b Backoff) NextInterval(attempt int) time.Duration {
	backoff := float64(b.initInterval) * (float64(attempt) * b.multiplier)
	if backoff > float64(b.maxInterval) {
		backoff = float64(b.maxInterval)
	}
	return time.Duration(backoff)
}
