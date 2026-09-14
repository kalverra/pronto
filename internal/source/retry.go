package source

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Retry defaults: 3 total attempts with exponential backoff starting at 500ms.
const (
	defaultRetryAttempts  = 3
	defaultRetryBaseDelay = 500 * time.Millisecond
	defaultRetryJitter    = 0.5
)

// RetryOption configures a retrying GraphQL client.
type RetryOption func(*retryConfig)

type retryConfig struct {
	attempts  int
	baseDelay time.Duration
	jitter    float64
	sleep     func(ctx context.Context, d time.Duration) error
}

// WithRetryAttempts sets the total number of attempts (initial try included).
func WithRetryAttempts(n int) RetryOption {
	return func(c *retryConfig) {
		c.attempts = n
	}
}

// WithRetryBaseDelay sets the backoff base; sleep grows exponentially per
// attempt (base, 2*base, 4*base, ...).
func WithRetryBaseDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) {
		c.baseDelay = d
	}
}

// WithRetryJitter configures the jitter fraction for retry delays (e.g. 0.5 for equal-jitter).
func WithRetryJitter(fraction float64) RetryOption {
	return func(c *retryConfig) {
		c.jitter = fraction
	}
}

// WithRetrySleep configures the sleep function used to wait between retry attempts.
func WithRetrySleep(fn func(ctx context.Context, d time.Duration) error) RetryOption {
	return func(c *retryConfig) {
		c.sleep = fn
	}
}

type retryAttemptsContextKey struct{}

// ContextWithRetryAttempts returns a context carrying an override for retry attempts.
func ContextWithRetryAttempts(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, retryAttemptsContextKey{}, n)
}

func retryAttemptsFromContext(ctx context.Context) (int, bool) {
	v, ok := ctx.Value(retryAttemptsContextKey{}).(int)
	return v, ok
}

// retryingGraphQLClient wraps a GraphQLClient, retrying transient server
// failures (5xx, 429) with exponential backoff. Non-retryable errors and
// context cancellation surface immediately.
type retryingGraphQLClient struct {
	inner GraphQLClient
	cfg   retryConfig
}

// defaultSleep waits for d to elapse or ctx to be canceled.
func defaultSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// NewRetryingGraphQLClient wraps inner with 5xx/429 retry behavior.
func NewRetryingGraphQLClient(inner GraphQLClient, opts ...RetryOption) GraphQLClient {
	cfg := retryConfig{
		attempts:  defaultRetryAttempts,
		baseDelay: defaultRetryBaseDelay,
		jitter:    defaultRetryJitter,
		sleep:     defaultSleep,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.attempts < 1 {
		cfg.attempts = 1
	}
	if cfg.sleep == nil {
		cfg.sleep = defaultSleep
	}
	return &retryingGraphQLClient{inner: inner, cfg: cfg}
}

// DoWithContext executes the query, retrying transient failures with
// exponential backoff. A Retry-After header, when present, extends the delay.
func (c *retryingGraphQLClient) DoWithContext(
	ctx context.Context,
	query string,
	variables map[string]any,
	response any,
) error {
	attempts := c.cfg.attempts
	if override, ok := retryAttemptsFromContext(ctx); ok && override > 0 {
		attempts = override
	}

	for attempt := 1; ; attempt++ {
		err := c.inner.DoWithContext(ctx, query, variables, response)
		if err == nil {
			return nil
		}
		if attempt >= attempts || !isRetryableErr(err) {
			return err
		}

		delay := c.cfg.baseDelay << (attempt - 1)
		if c.cfg.jitter > 0 {
			f := min(max(c.cfg.jitter, 0), 1)
			// #nosec G404 -- retry jitter does not require cryptographic randomness
			delay = time.Duration(
				float64(delay) * (1.0 - f + f*rand.Float64()),
			)
		}
		if after, _ := retryAfter(err); after > delay {
			delay = after
		}

		if err := c.cfg.sleep(ctx, delay); err != nil {
			return err
		}
	}
}

// ErrPrimaryRateLimit reports an exhausted GitHub GraphQL primary rate limit
// (the hourly points budget). The limit is account-global: no further
// requests should be issued until the observed reset time passes.
var ErrPrimaryRateLimit = errors.New("github primary rate limit exceeded")

// isPrimaryRateLimitErr reports whether err is a GitHub primary rate limit
// response: a 403/429 whose message names a rate limit but not the secondary
// one. These responses carry no useful Retry-After, so the backoff window
// comes from the rateLimit block observed on earlier successful responses.
func isPrimaryRateLimitErr(err error) bool {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode != http.StatusForbidden && httpErr.StatusCode != http.StatusTooManyRequests {
		return false
	}
	msg := strings.ToLower(httpErr.Message)
	return strings.Contains(msg, "rate limit") && !strings.Contains(msg, "secondary rate limit")
}

// ErrSecondaryRateLimit reports a GitHub secondary rate limit response. The
// limit is account-global, so no further requests should be issued for this
// fetch; GitHub's docs direct waiting at least one minute before retrying and
// warn that continuing to make requests while limited risks a ban.
var ErrSecondaryRateLimit = errors.New("github secondary rate limit exceeded")

// isSecondaryRateLimitErr reports whether err is a GitHub secondary rate
// limit response: a 403/429 whose message names the secondary rate limit.
// GitHub may omit the Retry-After header on these responses; when omitted,
// status-code checks alone cannot distinguish them from auth failures. When
// Retry-After is present on a 403, isRetryableHTTPErr retries it instead.
func isSecondaryRateLimitErr(err error) bool {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode != http.StatusForbidden && httpErr.StatusCode != http.StatusTooManyRequests {
		return false
	}
	return strings.Contains(strings.ToLower(httpErr.Message), "secondary rate limit")
}

// isRetryableHTTPErr reports whether the error is a transient server failure
// worth retrying: 5xx, 429, or a 403 carrying a Retry-After header (which
// GitHub includes when specifying an explicit backoff window, whereas auth
// failures carry none).
func isRetryableHTTPErr(err error) bool {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode >= 500 || httpErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if httpErr.StatusCode == http.StatusForbidden {
		_, ok := retryAfter(err)
		return ok
	}
	return false
}

// isRetryableErr reports whether err is a transient failure worth retrying:
// - HTTP 5xx, 429, or 403 with Retry-After header
// - Network timeouts (net.Error with Timeout() == true)
// - Connection resets (ECONNRESET) or refusals (ECONNREFUSED)
// - Unexpected EOF (io.ErrUnexpectedEOF)
// Context cancellation and deadlines are never retryable.
func isRetryableErr(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// A primary rate limit is exhausted until the hourly window resets:
	// retrying within the fetch cannot succeed, whatever headers say.
	if isPrimaryRateLimitErr(err) {
		return false
	}
	if isRetryableHTTPErr(err) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// retryAfter extracts a Retry-After header (seconds) from a GitHub HTTP error.
func retryAfter(err error) (time.Duration, bool) {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return 0, false
	}
	secs, err := strconv.Atoi(httpErr.Headers.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}
