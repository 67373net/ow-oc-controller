package clash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Client struct {
	baseURL    string
	secret     string
	httpClient *http.Client
}

type VersionResponse struct {
	Version string `json:"version"`
	Premium bool   `json:"premium,omitempty"`
	Meta    bool   `json:"meta,omitempty"`
}

type ConfigsResponse struct {
	Port       int    `json:"port"`
	SocksPort  int    `json:"socks-port"`
	RedirPort  int    `json:"redir-port"`
	TproxyPort int    `json:"tproxy-port"`
	MixedPort  int    `json:"mixed-port"`
	Mode       string `json:"mode"`
	LogLevel   string `json:"log-level"`
}

type ProxyHistory struct {
	Time  string `json:"time"`
	Delay int    `json:"delay"`
}

type ProxyItem struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Now     string         `json:"now,omitempty"`
	All     []string       `json:"all,omitempty"`
	History []ProxyHistory `json:"history,omitempty"`
	UDP     bool           `json:"udp,omitempty"`
}

type ProxiesResponse struct {
	Proxies map[string]ProxyItem `json:"proxies"`
}

type ProviderItem struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	VehicleType string      `json:"vehicleType"`
	UpdatedAt   string      `json:"updatedAt"`
	Proxies     []ProxyItem `json:"proxies"`
}

type ProvidersResponse struct {
	Providers map[string]ProviderItem `json:"providers"`
}

type TrafficResponse struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

func NewClient(baseURL, secret string) *Client {
	transport := &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	return &Client{
		baseURL: baseURL,
		secret:  secret,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
	}
}

func (c *Client) request(ctx context.Context, method, endpoint string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, reqBody)
	if err != nil {
		return nil, err
	}

	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// GetVersion checks connection to Clash/Mihomo core
func (c *Client) GetVersion(ctx context.Context) (*VersionResponse, error) {
	resp, err := c.request(ctx, http.MethodGet, "/version", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var res VersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetConfigs returns the current core configs including running mode
func (c *Client) GetConfigs(ctx context.Context) (*ConfigsResponse, error) {
	resp, err := c.request(ctx, http.MethodGet, "/configs", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var res ConfigsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SetMode updates running mode: "Rule", "Global", or "Direct"
func (c *Client) SetMode(ctx context.Context, mode string) error {
	payload := map[string]string{"mode": mode}
	resp, err := c.request(ctx, http.MethodPatch, "/configs", payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to set mode (code %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// GetProxies gets all proxies and groups
func (c *Client) GetProxies(ctx context.Context) (*ProxiesResponse, error) {
	resp, err := c.request(ctx, http.MethodGet, "/proxies", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var res ProxiesResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SelectProxy switches the active node for a specific group
func (c *Client) SelectProxy(ctx context.Context, group, proxyName string) error {
	endpoint := fmt.Sprintf("/proxies/%s", url.PathEscape(group))
	payload := map[string]string{"name": proxyName}
	resp, err := c.request(ctx, http.MethodPut, endpoint, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to select proxy (code %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// TestDelay tests the latency of a single node or group
func (c *Client) TestDelay(ctx context.Context, proxyName, testURL string, timeoutMs int) (int, error) {
	if testURL == "" {
		testURL = "http://www.gstatic.com/generate_204"
	}
	if timeoutMs <= 0 {
		timeoutMs = 3000
	}

	endpoint := fmt.Sprintf("/proxies/%s/delay?url=%s&timeout=%d",
		url.PathEscape(proxyName),
		url.QueryEscape(testURL),
		timeoutMs,
	)

	// Custom client with timeout for delay test
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return 0, err
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}

	client := &http.Client{Timeout: time.Duration(timeoutMs+1000) * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("delay test failed with status %d", resp.StatusCode)
	}

	var delayRes struct {
		Delay int `json:"delay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&delayRes); err != nil {
		return 0, err
	}
	return delayRes.Delay, nil
}

// BatchTestDelay runs concurrent delay tests for multiple nodes
func (c *Client) BatchTestDelay(ctx context.Context, nodes []string, testURL string, timeoutMs int) map[string]int {
	results := make(map[string]int)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Concurrency limit to prevent overwhelming the router
	sem := make(chan struct{}, 10)

	for _, node := range nodes {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			delay, err := c.TestDelay(ctx, name, testURL, timeoutMs)
			mu.Lock()
			if err != nil {
				results[name] = -1 // -1 signifies timeout/error
			} else {
				results[name] = delay
			}
			mu.Unlock()
		}(node)
	}

	wg.Wait()
	return results
}

// GetProviders retrieves all proxy providers (subscriptions)
func (c *Client) GetProviders(ctx context.Context) (*ProvidersResponse, error) {
	resp, err := c.request(ctx, http.MethodGet, "/providers/proxies", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var res ProvidersResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateProvider triggers an update/pull for a provider subscription
func (c *Client) UpdateProvider(ctx context.Context, providerName string) error {
	endpoint := fmt.Sprintf("/providers/proxies/%s", url.PathEscape(providerName))
	resp, err := c.request(ctx, http.MethodPut, endpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update provider (code %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// ReloadConfig forces Clash core to reload configuration from a specific path
func (c *Client) ReloadConfig(ctx context.Context, configPath string) error {
	payload := map[string]any{"path": configPath}
	resp, err := c.request(ctx, http.MethodPut, "/configs?force=true", payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to reload config (code %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

type ConnectionsResponse struct {
	DownloadTotal int64                   `json:"downloadTotal"`
	UploadTotal   int64                   `json:"uploadTotal"`
	Connections   []ConnectionDetailsItem `json:"connections"`
}

type ConnectionDetailsItem struct {
	ID       string `json:"id"`
	Metadata struct {
		Network         string `json:"network"`
		Type            string `json:"type"`
		SourceIP        string `json:"sourceIP"`
		DestinationIP   string `json:"destinationIP"`
		SourcePort      string `json:"sourcePort"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
	} `json:"metadata"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Start    string   `json:"start"`
	Chains   []string `json:"chains"`
	Rule     string   `json:"rule"`
}

// GetConnections retrieves active and snapshot connections from Clash Core
func (c *Client) GetConnections(ctx context.Context) (*ConnectionsResponse, error) {
	resp, err := c.request(ctx, http.MethodGet, "/connections", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var res ConnectionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateConfig updates baseURL and secret for hot-reloading
func (c *Client) UpdateConfig(baseURL, secret string) {
	c.baseURL = baseURL
	c.secret = secret
}



