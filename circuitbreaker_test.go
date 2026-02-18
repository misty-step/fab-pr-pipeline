package main

import (
	"testing"
)

func TestCircuitBreaker(t *testing.T) {
	t.Run("NewCircuitBreaker", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 5)
		if cb == nil {
			t.Fatal("NewCircuitBreaker returned nil")
		}
		if cb.failureThreshold != 3 {
			t.Errorf("expected failureThreshold=3, got %d", cb.failureThreshold)
		}
		if cb.skipRuns != 5 {
			t.Errorf("expected skipRuns=5, got %d", cb.skipRuns)
		}
	})

	t.Run("IsOpen returns false initially", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 5)
		if cb.IsOpen("https://github.com/test/repo/pull/1") {
			t.Error("IsOpen should return false for new PR")
		}
	})

	t.Run("Circuit opens after threshold failures", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 5)
		url := "https://github.com/test/repo/pull/1"

		// Record 3 failures
		cb.RecordFailure(url)
		if cb.IsOpen(url) {
			t.Error("Circuit should not be open after 1 failure")
		}
		cb.RecordFailure(url)
		if cb.IsOpen(url) {
			t.Error("Circuit should not be open after 2 failures")
		}
		cb.RecordFailure(url)
		// After 3 failures, circuit should be open on next check
		if !cb.IsOpen(url) {
			t.Error("Circuit should be open after 3 failures")
		}
	})

	t.Run("Success resets failure count", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 5)
		url := "https://github.com/test/repo/pull/1"

		cb.RecordFailure(url)
		cb.RecordFailure(url)
		cb.RecordSuccess(url)
		cb.RecordFailure(url)

		if cb.IsOpen(url) {
			t.Error("Circuit should not be open - failure count was reset")
		}
	})

	t.Run("Skip counter decrements", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 2) // Skip for 2 runs
		url := "https://github.com/test/repo/pull/1"

		// Open the circuit
		cb.RecordFailure(url)
		cb.RecordFailure(url)
		cb.RecordFailure(url)

		// First skip (counter goes from 2 to 1)
		if !cb.IsOpen(url) {
			t.Error("Expected circuit to be open (first skip)")
		}
		// Second skip (counter goes from 1 to 0)
		if !cb.IsOpen(url) {
			t.Error("Expected circuit to be open (second skip)")
		}
		// Third check - circuit should be closed now
		if cb.IsOpen(url) {
			t.Error("Expected circuit to be closed after skip period expired")
		}
	})

	t.Run("RecordSuccess closes open circuit", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 5)
		url := "https://github.com/test/repo/pull/1"

		// Open the circuit
		for i := 0; i < 3; i++ {
			cb.RecordFailure(url)
		}
		// Verify it's open
		if !cb.IsOpen(url) {
			t.Fatal("Circuit should be open")
		}

		// Record success - this should close the circuit immediately
		cb.RecordSuccess(url)

		// Circuit should be closed
		if cb.IsOpen(url) {
			t.Error("Circuit should be closed after success")
		}
	})

	t.Run("Different PRs have independent state", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 5)
		url1 := "https://github.com/test/repo/pull/1"
		url2 := "https://github.com/test/repo/pull/2"

		// Open circuit for url1
		for i := 0; i < 3; i++ {
			cb.RecordFailure(url1)
		}

		if !cb.IsOpen(url1) {
			t.Error("Circuit should be open for url1")
		}
		if cb.IsOpen(url2) {
			t.Error("Circuit should not be open for url2")
		}
	})

	t.Run("Logs circuit open message", func(t *testing.T) {
		// This test verifies the function doesn't panic when logging
		cb := NewCircuitBreaker(3, 5)
		url := "https://github.com/test/repo/pull/1"

		// Open the circuit - should log
		for i := 0; i < 3; i++ {
			cb.RecordFailure(url)
		}
		// No assertion on output, just verifying no panic
	})
}

func TestCircuitBreakerConcurrency(t *testing.T) {
	cb := NewCircuitBreaker(3, 5)
	url := "https://github.com/test/repo/pull/1"

	// Run concurrent operations to test thread safety
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			cb.RecordFailure(url)
			cb.IsOpen(url)
			cb.RecordSuccess(url)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// If we get here without deadlock or panic, the mutex is working
}

// BenchmarkCircuitBreaker measures the performance of circuit breaker operations
func BenchmarkCircuitBreaker(b *testing.B) {
	cb := NewCircuitBreaker(3, 5)
	urls := []string{
		"https://github.com/test/repo/pull/1",
		"https://github.com/test/repo/pull/2",
		"https://github.com/test/repo/pull/3",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		url := urls[i%len(urls)]
		if i%4 == 0 {
			cb.RecordSuccess(url)
		} else {
			cb.RecordFailure(url)
		}
		cb.IsOpen(url)
	}
}
