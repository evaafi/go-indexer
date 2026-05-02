package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/evaafi/go-indexer/config"
	"golang.org/x/time/rate"
)

const (
	tonCenterBaseURL    = "https://toncenter.com"
	tonCenterTimeout    = 30 * time.Second
	tonCenterMaxRetries = 5
	tonCenterMaxBackoff = 30 * time.Second
)

var (
	tcLimiter     *rate.Limiter
	tcLimiterOnce sync.Once
	tcClient      = &http.Client{Timeout: tonCenterTimeout}
)

// errEmptyResult signals that the request succeeded but the requested resource
// was missing. Callers may treat this as a non-retryable empty response.
var errEmptyResult = errors.New("toncenter: empty result")

// tonCenterLimiter returns a process-wide rate.Limiter built from
// config.CFG.TonCenterRPS / TonCenterBurst. Both endpoints share the same
// token bucket so we never burst past TonCenter's per-key budget. Defaults are
// conservative (1 rps, burst 1) if config is unset.
func tonCenterLimiter() *rate.Limiter {
	tcLimiterOnce.Do(func() {
		rps := config.CFG.TonCenterRPS
		burst := config.CFG.TonCenterBurst
		if rps <= 0 {
			rps = 1
		}
		if burst <= 0 {
			burst = int(rps)
			if burst < 1 {
				burst = 1
			}
		}
		tcLimiter = rate.NewLimiter(rate.Limit(rps), burst)
	})
	return tcLimiter
}

// tcGet performs a rate-limited GET against TonCenter with retries on 429/5xx
// (honoring Retry-After) and exponential backoff with jitter on transport
// errors. The response body is decoded into out. ctx is honored at every wait
// point.
func tcGet(ctx context.Context, path string, q url.Values, out interface{}) error {
	u, err := url.Parse(tonCenterBaseURL + path)
	if err != nil {
		return fmt.Errorf("toncenter url parse: %w", err)
	}
	u.RawQuery = q.Encode()

	var lastErr error
	for attempt := 0; attempt < tonCenterMaxRetries; attempt++ {
		if err := tonCenterLimiter().Wait(ctx); err != nil {
			return fmt.Errorf("toncenter rate wait: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return fmt.Errorf("toncenter new request: %w", err)
		}
		if config.CFG.TonCenterAPIKey != "" {
			req.Header.Set("X-API-Key", config.CFG.TonCenterAPIKey)
		}
		req.Header.Set("Accept", "application/json")

		resp, doErr := tcClient.Do(req)
		if doErr != nil {
			lastErr = fmt.Errorf("toncenter do: %w", doErr)
			if err := sleepWithCtx(ctx, backoffDelay(attempt)); err != nil {
				return err
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("toncenter read body: %w", readErr)
			if err := sleepWithCtx(ctx, backoffDelay(attempt)); err != nil {
				return err
			}
			continue
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("toncenter http %d: %s", resp.StatusCode, truncate(string(body), 256))
			delay := parseRetryAfter(resp.Header.Get("Retry-After"))
			if delay == 0 {
				delay = backoffDelay(attempt)
			}
			if err := sleepWithCtx(ctx, delay); err != nil {
				return err
			}
			continue
		case resp.StatusCode == http.StatusNotFound:
			return errEmptyResult
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			return fmt.Errorf("toncenter http %d: %s", resp.StatusCode, truncate(string(body), 256))
		}

		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("toncenter unmarshal: %w (body=%s)", err, truncate(string(body), 256))
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("toncenter: exhausted retries")
	}
	return fmt.Errorf("toncenter exhausted %d retries: %w", tonCenterMaxRetries, lastErr)
}

func backoffDelay(attempt int) time.Duration {
	base := 500 * time.Millisecond
	d := base << attempt
	if d <= 0 || d > tonCenterMaxBackoff {
		d = tonCenterMaxBackoff
	}
	jitter := time.Duration(rand.Int63n(int64(d)/4 + 1))
	return d + jitter
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func sleepWithCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// ---- TonCenter endpoints ----

// /api/v3/accountStates response (single + batch shapes).
type tcAccountStatesResponse struct {
	Accounts []struct {
		Address string `json:"address"`
		DataBoc string `json:"data_boc"`
		Status  string `json:"status"`
	} `json:"accounts"`
	AddressBook map[string]struct {
		UserFriendly string `json:"user_friendly"`
	} `json:"address_book"`
	Error string `json:"error"`
}

// GetAccountStateToncenter fetches the account-storage data BOC (base64) for a
// single address. Returns an empty string with nil error if the account is
// uninitialized or has no storage data — caller should treat that as "skip"
// rather than an error.
func GetAccountStateToncenter(ctx context.Context, addr string) (string, error) {
	q := url.Values{}
	q.Set("address", addr)
	q.Set("include_boc", "true")

	var resp tcAccountStatesResponse
	if err := tcGet(ctx, "/api/v3/accountStates", q, &resp); err != nil {
		if errors.Is(err, errEmptyResult) {
			return "", nil
		}
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("toncenter error: %s", resp.Error)
	}
	if len(resp.Accounts) == 0 {
		return "", nil
	}
	for _, acc := range resp.Accounts {
		if acc.DataBoc != "" {
			return acc.DataBoc, nil
		}
	}
	return "", nil
}

// FetchAccountStatesToncenter returns a map[address]data_boc for the given
// addresses in a single request. The map is keyed by both the raw and
// user-friendly forms (when address_book lookup is available) so callers can
// look up by either form.
func FetchAccountStatesToncenter(ctx context.Context, addresses []string) (map[string]string, error) {
	if len(addresses) == 0 {
		return map[string]string{}, nil
	}

	q := url.Values{}
	for _, a := range addresses {
		q.Add("address", a)
	}
	q.Set("include_boc", "true")

	var resp tcAccountStatesResponse
	if err := tcGet(ctx, "/api/v3/accountStates", q, &resp); err != nil {
		if errors.Is(err, errEmptyResult) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("toncenter error: %s", resp.Error)
	}

	out := make(map[string]string, len(resp.Accounts)*2)
	for _, acc := range resp.Accounts {
		if acc.DataBoc == "" {
			continue
		}
		out[acc.Address] = acc.DataBoc
		if ab, ok := resp.AddressBook[acc.Address]; ok && ab.UserFriendly != "" {
			out[ab.UserFriendly] = acc.DataBoc
		}
	}
	return out, nil
}
