package indexer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type tonCenterAccountStatesResponse struct {
	Accounts []struct {
		Address string `json:"address"`
		DataBoc string `json:"data_boc"`
	} `json:"accounts"`
	AddressBook map[string]struct {
		UserFriendly string `json:"user_friendly"`
	} `json:"address_book"`
}


// small helper to avoid importing context everywhere
func contextBackground() rateWaitContext { return rateWaitContext{} }

type rateWaitContext struct{}

func (rateWaitContext) Deadline() (deadline time.Time, ok bool) { return }
func (rateWaitContext) Done() <-chan struct{}                   { return nil }
func (rateWaitContext) Err() error                              { return nil }
func (rateWaitContext) Value(key interface{}) interface{}       { return nil }

// fetchTonCenterAccountStatesWithBaseURL calls TON Center /api/v3/accountStates and returns map[contractAddress]data_boc.
func fetchTonCenterAccountStatesWithBaseURL(baseURL string, apiKey string, addresses []string) (map[string]string, error) {
	if len(addresses) == 0 {
		return map[string]string{}, nil
	}
	// apply rate limiter
	// beforeTonCenterCall()

	// build URL with query params
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v3/accountStates"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse endpoint %s: %w", endpoint, err)
	}
	q := u.Query()
	for _, a := range addresses {
		q.Add("address", a)
	}
	q.Set("include_boc", "true")
	if apiKey != "" {
		// also add api_key as query param for compatibility with deployments expecting it in query
		q.Set("api_key", apiKey)
	}
	u.RawQuery = q.Encode()

	// sanitized URL for logs (no api_key)
	safeURL := *u
	qSafe := safeURL.Query()
	qSafe.Del("api_key")
	safeURL.RawQuery = qSafe.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request GET %s: %w", safeURL.String()[0:100], err)
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", safeURL.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s status %d body: %s", safeURL.String(), resp.StatusCode, string(b))
	}

	var parsed tonCenterAccountStatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(parsed.Accounts))
	for _, acc := range parsed.Accounts {
		result[acc.Address] = acc.DataBoc
		if ab, ok := parsed.AddressBook[acc.Address]; ok && ab.UserFriendly != "" {
			result[ab.UserFriendly] = acc.DataBoc
		}
	}
	return result, nil
}

func fetchTonCenterAccountStates(apiKey string, addresses []string) (map[string]string, error) {
	return fetchTonCenterAccountStatesWithBaseURL("https://toncenter.com", apiKey, addresses)
}
