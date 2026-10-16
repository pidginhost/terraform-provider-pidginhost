package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	pidginhost "github.com/pidginhost/sdk-go"
)

const defaultBaseURL = "https://www.pidginhost.com"

// pollInterval is how often waits ask the API for progress. Tests shorten it.
var pollInterval = 5 * time.Second

// parseID reads the numeric id the API uses in its paths.
func parseID(id string) (int32, error) {
	n, err := strconv.ParseInt(id, 10, 32)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a numeric PidginHost id", id)
	}
	return int32(n), nil
}

// maxPages stops a list walk that never reaches its last page.
const maxPages = 1000

// newAPIClient builds the SDK client the provider hands to every resource.
func newAPIClient(token, baseURL, userAgent string) *pidginhost.APIClient {
	cfg := pidginhost.NewConfiguration()
	cfg.Servers = pidginhost.ServerConfigurations{{URL: strings.TrimRight(baseURL, "/")}}
	cfg.UserAgent = userAgent
	cfg.AddDefaultHeader("Authorization", "Token "+token)
	cfg.HTTPClient = &http.Client{
		Timeout:   2 * time.Minute,
		Transport: &retryTransport{base: http.DefaultTransport, attempts: 5, wait: 2 * time.Second},
	}
	return pidginhost.NewAPIClient(cfg)
}

// retryTransport repeats a request the API did not process: any request it
// throttled (429), and reads that hit a transient server or network error.
// Other writes are never repeated, since a POST that timed out may have run.
type retryTransport struct {
	base     http.RoundTripper
	attempts int
	wait     time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	read := req.Method == http.MethodGet || req.Method == http.MethodHead
	for attempt := 1; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		throttled := err == nil && resp.StatusCode == http.StatusTooManyRequests
		shouldRetry := throttled || read && retryable(resp, err)
		if attempt >= t.attempts || !shouldRetry {
			return resp, err
		}
		if req.Body != nil && req.Body != http.NoBody {
			if req.GetBody == nil {
				return resp, err
			}
			body, bodyErr := req.GetBody()
			if bodyErr != nil {
				return resp, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
		wait := t.wait * time.Duration(attempt)
		if resp != nil {
			if after := retryAfter(resp.Header.Get("Retry-After")); after > 0 {
				wait = after
			}
			_ = resp.Body.Close()
		}
		select {
		case <-req.Context().Done():
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, req.Context().Err()
		case <-time.After(wait):
		}
	}
}

func retryable(resp *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryAfter reads a Retry-After header given in seconds or as an HTTP date.
func retryAfter(value string) time.Duration {
	if s, err := strconv.Atoi(value); err == nil {
		return time.Duration(s) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return time.Until(at)
	}
	return 0
}

// isNotFound reports whether an SDK call failed because the object is gone.
func isNotFound(resp *http.Response, err error) bool {
	return err != nil && resp != nil && resp.StatusCode == http.StatusNotFound
}

// apiError turns an SDK error into a diagnostic that keeps the API's own
// explanation, which the SDK only keeps in the raw body.
func apiError(summary string, err error) diag.Diagnostic {
	return diag.NewErrorDiagnostic(summary, describeError(err))
}

// secretRouteError is apiError for endpoints whose response body is itself a
// secret: a body that failed to decode would otherwise quote the keys into
// the Terraform output and logs. SDK decoding errors can quote the body too.
func secretRouteError(summary string, resp *http.Response, err error) diag.Diagnostic {
	detail := "The request failed"
	switch {
	case errors.Is(err, context.Canceled):
		detail = "The request was canceled"
	case errors.Is(err, context.DeadlineExceeded):
		detail = "The request timed out"
	case resp != nil:
		detail = fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		if resp.StatusCode < 300 {
			detail += ": the response could not be read"
		}
	}
	return diag.NewErrorDiagnostic(summary, detail+" (response details withheld: they can carry credentials)")
}

func describeError(err error) string {
	var apiErr *pidginhost.GenericOpenAPIError
	if errors.As(err, &apiErr) {
		if msg := formatAPIBody(apiErr.Body()); msg != "" {
			return fmt.Sprintf("%s: %s", apiErr.Error(), msg)
		}
	}
	return err.Error()
}

// formatAPIBody flattens a DRF error body ({"field": ["msg"], "detail": "msg"})
// into one line, field names first so the user sees which input was refused.
func formatAPIBody(body []byte) string {
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		text := strings.TrimSpace(string(body))
		if len(text) > 500 || strings.HasPrefix(text, "<") {
			return ""
		}
		return text
	}
	parts := flatten("", parsed)
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func flatten(prefix string, value any) []string {
	switch v := value.(type) {
	case map[string]any:
		var out []string
		for k, item := range v {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			if k == "detail" || k == "message" || k == "non_field_errors" {
				key = prefix
			}
			out = append(out, flatten(key, item)...)
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, flatten(prefix, item)...)
		}
		return out
	case nil:
		return nil
	default:
		if prefix == "" {
			return []string{fmt.Sprint(v)}
		}
		return []string{fmt.Sprintf("%s: %v", prefix, v)}
	}
}

// fetchAll walks a paginated list endpoint. fetch returns one page and
// whether another page follows.
func fetchAll[T any](fetch func(page int32) ([]T, bool, error)) ([]T, error) {
	var all []T
	for page := int32(1); page <= maxPages; page++ {
		items, more, err := fetch(page)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if !more {
			return all, nil
		}
	}
	return nil, fmt.Errorf("list did not end after %d pages", maxPages)
}

// waitFor polls check until it reports done, fails, or ctx expires.
func waitFor(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}
