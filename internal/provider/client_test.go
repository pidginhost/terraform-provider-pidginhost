package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormatAPIBodyNamesTheRefusedField(t *testing.T) {
	got := formatAPIBody([]byte(`{"hostname": ["This field is required."], "detail": "Bad input"}`))
	want := "Bad input; hostname: This field is required."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatAPIBodyDropsHTMLPages(t *testing.T) {
	if got := formatAPIBody([]byte("<html>502 Bad Gateway</html>")); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestDescribeErrorKeepsTheAPIBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name": ["already exists"]}`))
	}))
	defer srv.Close()

	client := newAPIClient("t", srv.URL, "test")
	_, resp, err := client.AccountAPI.AccountSshKeysList(context.Background()).Execute()
	if err == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected a 400 error, got %v", err)
	}
	if got := describeError(err); got != "400 Bad Request: name: already exists" {
		t.Fatalf("got %q", got)
	}
}

func TestClientSendsTokenAndUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret-value" || r.Header.Get("User-Agent") != "ua/1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count": 0, "results": []}`))
	}))
	defer srv.Close()

	_, _, err := newAPIClient("secret-value", srv.URL+"/", "ua/1").AccountAPI.AccountSshKeysList(context.Background()).Execute()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetryTransportRepeatsReadsOnly(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := &http.Client{Transport: &retryTransport{base: http.DefaultTransport, attempts: 5, wait: time.Millisecond}}

	resp, err := client.Get(srv.URL)
	if err != nil || resp.StatusCode != http.StatusOK || calls.Load() != 3 {
		t.Fatalf("GET: status %v err %v calls %d", resp, err, calls.Load())
	}
	_ = resp.Body.Close()

	calls.Store(0)
	resp, err = client.Post(srv.URL, "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatalf("POST must not be repeated: status %v err %v calls %d", resp, err, calls.Load())
	}
	_ = resp.Body.Close()
}

func TestRetryTransportReplaysThrottledWrites(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		for _, body := range []string{"", `{"project":"example"}`} {
			t.Run(method+body, func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got, _ := io.ReadAll(r.Body)
					if string(got) != body {
						t.Errorf("request body changed on retry")
					}
					if calls.Add(1) == 1 {
						w.WriteHeader(http.StatusTooManyRequests)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				defer srv.Close()
				var reader io.Reader
				if body != "" {
					reader = strings.NewReader(body)
				}
				req, err := http.NewRequestWithContext(t.Context(), method, srv.URL, reader)
				if err != nil {
					t.Fatal(err)
				}
				client := &http.Client{Transport: &retryTransport{base: http.DefaultTransport, attempts: 3, wait: time.Millisecond}}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusNoContent || calls.Load() != 2 {
					t.Fatalf("status %d, requests %d; want one successful retry", resp.StatusCode, calls.Load())
				}
			})
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetryTransportDoesNotRepeatUnconfirmedWrites(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		for _, status := range []int{0, 400, 409, 500, 502, 503, 504} {
			t.Run(fmt.Sprintf("%s/%d", method, status), func(t *testing.T) {
				calls := 0
				transport := &retryTransport{attempts: 3, base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					_ = req.Body.Close()
					if status == 0 {
						return nil, errors.New("connection lost after sending request")
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
				})}
				req, err := http.NewRequestWithContext(t.Context(), method, "https://example.com", strings.NewReader(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				resp, err := transport.RoundTrip(req)
				if resp != nil {
					_ = resp.Body.Close()
				}
				if calls != 1 || (status == 0 && err == nil) || (status != 0 && (err != nil || resp.StatusCode != status)) {
					t.Fatalf("write outcome changed: calls=%d error=%v", calls, err)
				}
			})
		}
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestRetryTransportClosesReplayBodyOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	replay := &trackedBody{Reader: strings.NewReader(`{}`)}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = func() (io.ReadCloser, error) { return replay, nil }
	transport := &retryTransport{attempts: 3, wait: time.Hour, base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		_ = req.Body.Close()
		cancel()
		return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	_, err = transport.RoundTrip(req)
	if !errors.Is(err, context.Canceled) || !replay.closed {
		t.Fatalf("canceled retry leaked body: closed=%t error=%v", replay.closed, err)
	}
}

func TestFetchAllWalksEveryPage(t *testing.T) {
	pages := map[int32][]int{1: {1, 2}, 2: {3}}
	got, err := fetchAll(func(page int32) ([]int, bool, error) {
		return pages[page], page < 2, nil
	})
	if err != nil || len(got) != 3 || got[2] != 3 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestWaitForStopsAtTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := waitFor(ctx, 5*time.Millisecond, func() (bool, error) { return false, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}
