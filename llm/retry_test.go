package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportReportsRetries(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	type retry struct {
		attempt int
		reason  string
	}
	var got []retry
	ctx := WithRetries(context.Background(), func(a int, r string) { got = append(got, retry{a, r}) })
	for range 3 {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		resp, err := HTTPClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	want := []retry{{1, "503 Service Unavailable"}, {2, "503 Service Unavailable"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Without WithRetries nothing is counted.
	req, _ := http.NewRequest("GET", srv.URL, nil)
	if resp, err := HTTPClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

func TestLogOmitsRequestBodiesAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: hello\n\n")
	}))
	defer srv.Close()
	var buf bytes.Buffer
	SetLog(&buf)
	defer SetLog(nil)
	req, _ := http.NewRequest("POST", srv.URL+"/chat", strings.NewReader(`{"secret prompt":1}`))
	req.Header.Set("Authorization", "Bearer sk-secret")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	got := buf.String()
	for _, want := range []string{"--> POST " + srv.URL + "/chat (19 bytes)", "<-- 200 OK", "data: hello"} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sk-secret") || strings.Contains(got, "secret prompt") {
		t.Errorf("log leaked the request:\n%s", got)
	}
}

// A server that accepts a request and never answers ends it, instead of hanging a -p run.
func TestASilentServerTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)
	c := &http.Client{Transport: Transport(WaitingTransport(50 * time.Millisecond))}
	start := time.Now()
	_, err := c.Get(srv.URL)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	if HTTPClient.Transport.(retryTransport).base.(*http.Transport).ResponseHeaderTimeout != HeaderTimeout {
		t.Fatal("the shared client does not wait HeaderTimeout")
	}
}
