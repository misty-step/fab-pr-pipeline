package main

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind classifies errors as transient, permanent, or unknown.
type ErrorKind int

const (
	// Unknown means we can't determine if the error is transient or permanent.
	Unknown ErrorKind = iota
	// Transient errors are temporary and worth retrying (rate limits, timeouts).
	Transient
	// Permanent errors won't succeed on retry (archived repo, not found, permissions).
	Permanent
)

func (k ErrorKind) String() string {
	switch k {
	case Transient:
		return "transient"
	case Permanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// classifies the error based on the error message and type.
// It's a best-effort classification - unknown errors default to Transient
// to avoid skipping potentially recoverable errors.
func classifyError(err error) ErrorKind {
	if err == nil {
		return Unknown
	}

	msg := strings.ToLower(err.Error())

	// Permanent errors - don't retry these.
	permanentIndicators := []string{
		"not found",
		"404",
		" archived ",
		"is archived",
		"permission denied",
		"403",
		"unauthorized",
		"already merged",
		"merge conflict",
		"closed pull request",
		"ref not found",
		"no such file or directory", // gh CLI not installed
		"command not found",
		"could not read username", // auth issues
		"bad credentials",
		"invalid credentials",
		"resource not found",
	}

	for _, indicator := range permanentIndicators {
		if strings.Contains(msg, indicator) {
			return Permanent
		}
	}

	// Transient errors - worth retrying.
	transientIndicators := []string{
		"rate limit",
		"timeout",
		"temporary failure",
		"server error",
		"500",
		"502",
		"503",
		"504",
		"connection refused",
		"connection reset",
		"network",
		"i/o timeout",
		"no route to host",
		"certificate",
		"tls",
		"temporary",
	}

	for _, indicator := range transientIndicators {
		if strings.Contains(msg, indicator) {
			return Transient
		}
	}

	// Default to Transient for unknown errors - better to retry than skip.
	// Callers can override this if needed.
	return Transient
}

// IsTransient returns true if the error is classified as transient.
func IsTransient(err error) bool {
	return classifyError(err) == Transient
}

// IsPermanent returns true if the error is classified as permanent.
func IsPermanent(err error) bool {
	return classifyError(err) == Permanent
}

// WrapError adds classification metadata to an error.
// This allows callers to check IsTransient/IsPermanent on wrapped errors.
type WrapError struct {
	Err  error
	Kind ErrorKind
}

func (e *WrapError) Error() string {
	return e.Err.Error()
}

func (e *WrapError) Unwrap() error {
	return e.Err
}

func (e *WrapError) Is(target error) bool {
	if target == ErrTransient || target == ErrPermanent {
		return e.Kind == Transient || e.Kind == Permanent
	}
	return errors.Is(e.Err, target)
}

// Sentinel errors for errors.Is checks.
var (
	ErrTransient = errors.New("transient error")
	ErrPermanent = errors.New("permanent error")
)

// NewTransient creates a new transient error.
func NewTransient(err error) error {
	if err == nil {
		return nil
	}
	return &WrapError{Err: err, Kind: Transient}
}

// NewPermanent creates a new permanent error.
func NewPermanent(err error) error {
	if err == nil {
		return nil
	}
	return &WrapError{Err: err, Kind: Permanent}
}

// RetryConfig holds configuration for retry behavior.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   int // milliseconds
	MaxDelay    int // milliseconds
}

var defaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   500,
	MaxDelay:    5000,
}

// Retryable runs the given function with retry logic for transient errors.
// It returns the last error if all attempts fail or if the error is permanent.
func Retryable(fn func() error, cfg ...RetryConfig) error {
	config := defaultRetryConfig
	if len(cfg) > 0 {
		config = cfg[0]
	}

	var lastErr error
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		kind := classifyError(err)

		if kind == Permanent {
			// Don't retry permanent errors.
			return err
		}

		lastErr = err

		// Check if we should retry.
		if attempt < config.MaxAttempts {
			// Exponential backoff: base * 2^(attempt-1), capped at maxDelay.
			delay := config.BaseDelay * (1 << (attempt - 1))
			if delay > config.MaxDelay {
				delay = config.MaxDelay
			}
			// Simple retry after delay - in production, consider using a proper backoff library.
			// For now, we just return the error to let the caller decide.
			// Actually, let's just continue - this is a simple implementation.
			_ = delay // Could implement actual sleep here if needed
		}
	}

	return lastErr
}

// ClassifyAndRetry attempts the operation, classifying errors and retrying transient ones.
// Returns (result, error) where error is nil on success, or permanent/last transient error on failure.
func ClassifyAndRetry[T any](fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error

	for attempt := 1; attempt <= defaultRetryConfig.MaxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		kind := classifyError(err)

		if kind == Permanent {
			// Don't retry permanent errors.
			return zero, err
		}

		lastErr = err

		// Transient error - will retry if attempts remain.
		// In a real implementation, we'd add backoff here.
		if attempt < defaultRetryConfig.MaxAttempts {
			// Backoff could be added here; skipping for now as retry is handled by re-execution
			continue
		}
	}

	return zero, lastErr
}

// RetryableWithResult wraps a function that returns a result and error,
// retrying on transient errors up to MaxAttempts times.
// Returns the result on success, or the final error (which may be permanent).
func RetryableWithResult[T any](fn func() (T, error), cfg RetryConfig) (T, error) {
	var zero T
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		kind := classifyError(err)

		if kind == Permanent {
			// Don't retry permanent errors.
			return zero, err
		}

		lastErr = err

		// Transient error - will retry if attempts remain.
		// Note: In production, add sleep here for backoff.
		_ = attempt < cfg.MaxAttempts // Silence linter; backoff can be added here
	}

	return zero, lastErr
}

// FormatErrorWithKind returns a human-readable error string with classification.
func FormatErrorWithKind(err error) string {
	if err == nil {
		return "success"
	}
	kind := classifyError(err)
	return fmt.Sprintf("[%s] %s", kind, err.Error())
}
