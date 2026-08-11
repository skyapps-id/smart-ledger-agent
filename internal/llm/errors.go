package llm

import "errors"

// ErrRateLimited menandakan HTTP 429 dari OpenRouter.
var ErrRateLimited = errors.New("rate limited oleh openrouter")

// RequestError membungkus error transport jaringan agar dapat di-retry.
type RequestError struct{ Err error }

func (e *RequestError) Error() string { return "kesalahan transport openrouter: " + e.Err.Error() }
func (e *RequestError) Unwrap() error { return e.Err }

// IsRetryable melaporkan apakah error layak di-retry (RFC §8.1).
func IsRetryable(err error) bool {
	var re *RequestError
	return errors.Is(err, ErrRateLimited) || errors.As(err, &re)
}
