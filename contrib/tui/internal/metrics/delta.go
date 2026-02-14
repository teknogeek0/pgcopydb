package metrics

import "time"

// DeltaCalculator tracks previous values and times per key
// to compute per-second rates of change.
type DeltaCalculator struct {
	prevValues map[string]int64
	prevTimes  map[string]time.Time
}

func NewDeltaCalculator() *DeltaCalculator {
	return &DeltaCalculator{
		prevValues: make(map[string]int64),
		prevTimes:  make(map[string]time.Time),
	}
}

// Rate returns the per-second rate of change for a named counter.
// Each key tracks its own previous value and timestamp independently.
func (d *DeltaCalculator) Rate(name string, current int64) float64 {
	prev, hasPrev := d.prevValues[name]
	prevTime, hasTime := d.prevTimes[name]
	now := time.Now()

	d.prevValues[name] = current
	d.prevTimes[name] = now

	if !hasPrev || !hasTime {
		return 0
	}

	elapsed := now.Sub(prevTime).Seconds()
	if elapsed <= 0 {
		return 0
	}

	rate := float64(current-prev) / elapsed
	if rate < 0 {
		return 0 // counter reset
	}
	return rate
}

// RateFloat64 works like Rate but for float64 counters (e.g. LSN positions).
func (d *DeltaCalculator) RateFloat64(name string, current float64) float64 {
	key := name + "_f64"
	prev, hasPrev := d.prevValues[key]
	prevTime, hasTime := d.prevTimes[key]
	now := time.Now()

	d.prevValues[key] = int64(current)
	d.prevTimes[key] = now

	if !hasPrev || !hasTime {
		return 0
	}

	elapsed := now.Sub(prevTime).Seconds()
	if elapsed <= 0 {
		return 0
	}

	rate := (current - float64(prev)) / elapsed
	if rate < 0 {
		return 0
	}
	return rate
}
