package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

	calls.Store(0)
	resp, err = client.Post(srv.URL, "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatalf("POST must not be repeated: status %v err %v calls %d", resp, err, calls.Load())
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
