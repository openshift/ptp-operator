package client

import (
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/golang/glog"
)

const (
	defaultMaxRetries   = 3
	retryBaseDelay      = 500 * time.Millisecond
	retryMaxDelay       = 5 * time.Second
	retryJitterFraction = 0.3
)

type retryRoundTripper struct {
	inner      http.RoundTripper
	maxRetries int
}

func wrapWithRetry(rt http.RoundTripper) http.RoundTripper {
	return &retryRoundTripper{inner: rt, maxRetries: defaultMaxRetries}
}

func (r *retryRoundTripper) canRetryBody(req *http.Request) bool {
	return req.Body == nil || req.GetBody != nil
}

func (r *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt)
			glog.Infof("retryRoundTripper: attempt %d/%d for %s %s after %v",
				attempt+1, r.maxRetries+1, req.Method, req.URL.Path, delay)

			timer := time.NewTimer(delay)
			select {
			case <-req.Context().Done():
				timer.Stop()
				return nil, req.Context().Err()
			case <-timer.C:
				timer.Stop()
			}
		}

		reqToAttempt := req
		if attempt > 0 && req.Body != nil {
			newBody, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			reqToAttempt = req.Clone(req.Context())
			reqToAttempt.Body = newBody
		}

		resp, err = r.inner.RoundTrip(reqToAttempt)

		if err != nil {
			if isRetryableTransportError(err) && attempt < r.maxRetries && r.canRetryBody(req) {
				continue
			}
			return nil, err
		}

		if isRetryableStatusCode(resp.StatusCode) && attempt < r.maxRetries && r.canRetryBody(req) {
			drainAndClose(resp)
			continue
		}

		return resp, nil
	}

	return resp, err
}

func isRetryableTransportError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, substr := range []string{
		"connection refused",
		"connection reset",
		"i/o timeout",
		"TLS handshake timeout",
		"unexpected EOF",
		"server misbehaving",
	} {
		if strings.Contains(s, substr) {
			return true
		}
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return true
	}
	return false
}

func isRetryableStatusCode(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func backoffDelay(attempt int) time.Duration {
	delay := float64(retryBaseDelay) * math.Pow(2, float64(attempt-1))
	if delay > float64(retryMaxDelay) {
		delay = float64(retryMaxDelay)
	}
	jitter := delay * retryJitterFraction * (rand.Float64()*2 - 1) //nolint:gosec // jitter for retry backoff, not security-sensitive
	return time.Duration(delay + jitter)
}
