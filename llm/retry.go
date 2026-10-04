package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Every SDK retries a failed request by sending it again with the same context, and says
// nothing. Transport counts the attempts a context makes and reports each retry to the
// function WithRetries attached, with why the previous attempt failed.

type retryKey struct{}

type retryState struct {
	mu       sync.Mutex
	attempts int
	last     string
	report   func(attempt int, reason string)
	// readErr is the last error reading a response body. SDK stream readers can drop it, so
	// a cut connection would otherwise look like a stream that ended.
	readErr error
}

// WithRetries returns a context whose requests report each retry: attempt counts from 1 for
// the first retry, and reason is why the attempt before it failed.
func WithRetries(ctx context.Context, report func(attempt int, reason string)) context.Context {
	return context.WithValue(ctx, retryKey{}, &retryState{report: report})
}

// Transport wraps base so requests made under WithRetries report their retries. A nil base
// means http.DefaultTransport.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return retryTransport{base}
}

// HTTPClient is a client with no overall timeout, since a stream is legitimately long, whose
// requests report their retries. It waits HeaderTimeout for a response to start.
var HTTPClient = &http.Client{Transport: Transport(WaitingTransport(HeaderTimeout))}

// HeaderTimeout bounds the wait for a response's headers. A streamed response sends them as it
// starts, so this ends only a request the server accepted and never answered. It is long
// because a local server may process a long prompt before it answers.
const HeaderTimeout = 10 * time.Minute

// WaitingTransport is http.DefaultTransport waiting at most wait for response headers.
func WaitingTransport(wait time.Duration) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = wait
	return t
}

type retryTransport struct{ base http.RoundTripper }

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s, _ := req.Context().Value(retryKey{}).(*retryState)
	if s == nil {
		return logged(req, t.base.RoundTrip)
	}
	s.mu.Lock()
	s.attempts++
	attempt, last := s.attempts, s.last
	s.mu.Unlock()
	if attempt > 1 {
		s.report(attempt-1, last)
	}

	resp, err := logged(req, t.base.RoundTrip)
	if err == nil {
		resp.Body = &watchedBody{ReadCloser: resp.Body, s: s}
	}
	var why string
	switch {
	case err != nil:
		why = firstLine(err.Error())
	case resp.StatusCode >= 400:
		why = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if why != "" {
		s.mu.Lock()
		s.last = why
		s.mu.Unlock()
	}
	return resp, err
}

// watchedBody records a read error other than EOF.
type watchedBody struct {
	io.ReadCloser
	s *retryState
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.s.mu.Lock()
		b.s.readErr = err
		b.s.mu.Unlock()
	}
	return n, err
}

// ErrIncomplete means a response stream ended before its finish: the connection dropped or
// the server closed it early. Nothing of the response is kept, so the request can be sent
// again.
var ErrIncomplete = errors.New("the response stream ended before it finished")

// Incomplete returns ErrIncomplete, with the read error that cut the stream when the
// transport saw one.
func Incomplete(ctx context.Context) error {
	if s, _ := ctx.Value(retryKey{}).(*retryState); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.readErr != nil {
			return fmt.Errorf("%w: %w", ErrIncomplete, s.readErr)
		}
	}
	return ErrIncomplete
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

var (
	logMu sync.Mutex
	logW  io.Writer
)

// SetLog writes each request's method, URL and size, its response status, and the raw
// response bytes to w, for diagnosing a provider. Request bodies and headers, keys among
// them, are never written. Nil turns it off.
func SetLog(w io.Writer) {
	logMu.Lock()
	logW = w
	logMu.Unlock()
}

func logf(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	if logW != nil {
		fmt.Fprintf(logW, format, args...)
	}
}

func logging() bool {
	logMu.Lock()
	defer logMu.Unlock()
	return logW != nil
}

// logged sends req, logging the exchange when a log is set.
func logged(req *http.Request, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if !logging() {
		return send(req)
	}
	logf("--> %s %s (%d bytes)\n", req.Method, req.URL.Redacted(), req.ContentLength)
	resp, err := send(req)
	if err != nil {
		logf("<-- error: %v\n", err)
		return resp, err
	}
	logf("<-- %s\n", resp.Status)
	resp.Body = &loggedBody{ReadCloser: resp.Body}
	return resp, nil
}

type loggedBody struct{ io.ReadCloser }

func (b *loggedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		logf("%s", p[:n])
	}
	if err != nil {
		logf("\n<-- end: %v\n", err)
	}
	return n, err
}
