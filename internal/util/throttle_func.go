package util

import (
	"time"
)

// ThrottledFunction takes a function and a duration, returning a new function
// that throttles calls to the original function.
func ThrottledFunction(interval time.Duration) func(jj func()) {
	// ticker := time.NewTicker(interval)
	lastCall := time.Now()
	return func(jj func()) {
		// Check if enough time has passed since the last call
		if time.Since(lastCall) >= interval {
			jj()
			lastCall = time.Now()
		} else {
			// Optionally, you can add logic here to handle throttled calls,
			// such as logging or returning an error.
			// fmt.Println("Function call throttled.")
		}
	}
}
