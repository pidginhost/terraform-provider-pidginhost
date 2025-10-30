package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultUserAgent            = "terraform-provider-pidginhost/dev"
	serverEndpointPath          = "/api/cloud/servers/"
	volumeEndpointPath          = "/api/cloud/volumes/"
	imagesEndpointPath          = "/api/cloud/images/"
	packagesEndpointPath        = "/api/cloud/server-packages/"
	profileEndpointPath         = "/api/account/profile"
	volumeProductsEndpoint      = "/api/cloud/storage-products/"
	sshKeysEndpointPath         = "/api/account/ssh-keys/"
	firewallEndpointPath        = "/api/cloud/firewall-rules-set/"
	ipv4EndpointPath            = "/api/cloud/ipv4/"
	ipv6EndpointPath            = "/api/cloud/ipv6/"
	publicInterfaceEndpointPath = "/api/cloud/public-interface/"
)

var errNotFound = errors.New("pidginhost: resource not found")

type API interface {
	CreateServer(ctx context.Context, payload ServerCreateRequest) (*Server, error)
	GetServer(ctx context.Context, id string) (*Server, error)
	DeleteServer(ctx context.Context, id string) error
	WaitForServerReady(ctx context.Context, id string, interval time.Duration) (*Server, error)
	WaitForServerDeletion(ctx context.Context, id string, interval time.Duration) error
	CreateServerVolume(ctx context.Context, serverID string, payload VolumeCreateRequest) (*Volume, error)
	UpdateVolume(ctx context.Context, id string, payload VolumeUpdateRequest) (*Volume, error)
	GetVolume(ctx context.Context, id string) (*Volume, error)
	DetachVolume(ctx context.Context, id, alias string) error
	DeleteVolume(ctx context.Context, id string) error
	WaitForVolumeDetached(ctx context.Context, id string, interval time.Duration) error
	WaitForVolumeDeletion(ctx context.Context, id string, interval time.Duration) error
	ListImages(ctx context.Context) ([]map[string]any, error)
	ListPackages(ctx context.Context) ([]map[string]any, error)
	GetProfile(ctx context.Context) (map[string]any, error)
	ListVolumeProducts(ctx context.Context) ([]map[string]any, error)
	ListVolumes(ctx context.Context) ([]map[string]any, error)
	ListSSHKeys(ctx context.Context) ([]map[string]any, error)
	ListFirewalls(ctx context.Context) ([]map[string]any, error)
	ListIPs(ctx context.Context) ([]map[string]any, error)
	ListIPv6(ctx context.Context) ([]map[string]any, error)
	ListPublicInterfaces(ctx context.Context) ([]map[string]any, error)
	ListServers(ctx context.Context) ([]map[string]any, error)
	CreateFirewallRulesSet(ctx context.Context, name string) (map[string]any, error)
	AddFirewallRule(ctx context.Context, firewallID string, rule map[string]any) error
	DeleteFirewallRulesSet(ctx context.Context, firewallID string) error
	FindFirewall(ctx context.Context, identifier string) (map[string]any, error)
	DetachIPv4(ctx context.Context, id string) error
	DeleteIPv4(ctx context.Context, id string) error
	DetachIPv6(ctx context.Context, id string) error
	DeleteIPv6(ctx context.Context, id string) error
}

// APIClient wraps HTTP interactions with the PidginHost API.
type APIClient struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func NewAPIClient(token, baseURL string) *APIClient {
	return &APIClient{
		token:   token,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *APIClient) request(ctx context.Context, method, path string, body any, expectedStatus ...int) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("%s%s", c.baseURL, path), bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if len(expectedStatus) == 0 {
		expectedStatus = []int{http.StatusOK}
	}

	for _, status := range expectedStatus {
		if resp.StatusCode == status {
			return resp, nil
		}
	}

	if resp.StatusCode == http.StatusNotFound {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil, errNotFound
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var apiErr struct {
		Message string `json:"message"`
		Detail  any    `json:"detail"`
		Extra   any    `json:"extra"`
	}
	if len(bodyBytes) > 0 {
		json.Unmarshal(bodyBytes, &apiErr)
	}

	msg := resp.Status
	if apiErr.Message != "" {
		detail := formatAny(apiErr.Detail)
		extra := formatAny(apiErr.Extra)
		msg = fmt.Sprintf("%s: %s", resp.Status, apiErr.Message)
		if detail != "" {
			msg = fmt.Sprintf("%s (%s)", msg, detail)
		}
		if extra != "" {
			msg = fmt.Sprintf("%s [%s]", msg, extra)
		}
	}

	return nil, fmt.Errorf("pidginhost API error: %s", msg)
}

// ServerCreateRequest mirrors the fields accepted by the PidginHost create server endpoint.
type ServerCreateRequest struct {
	Image                 string  `json:"image"`
	Package               string  `json:"package"`
	Hostname              string  `json:"hostname"`
	Project               *string `json:"project,omitempty"`
	Password              *string `json:"password,omitempty"`
	SSHPubKey             *string `json:"ssh_pub_key,omitempty"`
	SSHPubKeyID           *string `json:"ssh_pub_key_id,omitempty"`
	PublicIP              *string `json:"public_ip,omitempty"`
	NewIPv4               *bool   `json:"new_ipv4,omitempty"`
	PublicIPv6            *string `json:"public_ipv6,omitempty"`
	NewIPv6               *bool   `json:"new_ipv6,omitempty"`
	FwRulesSet            *string `json:"fw_rules_set,omitempty"`
	FwPolicyIn            *string `json:"fw_policy_in,omitempty"`
	FwPolicyOut           *string `json:"fw_policy_out,omitempty"`
	PrivateNetwork        *string `json:"private_network,omitempty"`
	PrivateAddress        *string `json:"private_address,omitempty"`
	NoNetworkAcknowledged *bool   `json:"no_network_acknowledged,omitempty"`
}

// Server represents the subset of server information needed by the provider.
type Server struct {
	ID       json.Number `json:"id"`
	Hostname string      `json:"hostname"`
	Status   string      `json:"status"`
	Machine  struct {
		Status string `json:"status"`
	} `json:"machine"`
}

func (s *Server) IDString() string {
	if s == nil {
		return ""
	}
	return s.ID.String()
}

type VolumeCreateRequest struct {
	Alias   string  `json:"alias"`
	Size    int64   `json:"size"`
	Product string  `json:"product"`
	Project *string `json:"project,omitempty"`
}

type Volume struct {
	ID       json.Number `json:"id"`
	Alias    string      `json:"alias"`
	Attached bool        `json:"attached"`
	Product  string      `json:"product"`
	Size     json.Number `json:"size"`
}

func (v *Volume) IDString() string {
	if v == nil {
		return ""
	}
	return v.ID.String()
}

type VolumeUpdateRequest struct {
	Alias   *string `json:"alias,omitempty"`
	Size    *int64  `json:"size,omitempty"`
	Product *string `json:"product,omitempty"`
	Project *string `json:"project,omitempty"`
}

func (s *Server) EffectiveStatus() string {
	if s == nil {
		return ""
	}
	if s.Machine.Status != "" {
		return s.Machine.Status
	}
	return s.Status
}

func (s *Server) IsReady() bool {
	if s == nil {
		return false
	}
	machineStatus := strings.ToLower(s.Machine.Status)
	status := strings.ToLower(s.Status)
	return machineStatus == "running" && status == "active"
}

func (c *APIClient) CreateServer(ctx context.Context, payload ServerCreateRequest) (*Server, error) {
	resp, err := c.request(ctx, http.MethodPost, serverEndpointPath, payload, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var server Server
	if err := json.NewDecoder(resp.Body).Decode(&server); err != nil {
		return nil, fmt.Errorf("failed to decode server create response: %w", err)
	}
	return &server, nil
}

func (c *APIClient) GetServer(ctx context.Context, id string) (*Server, error) {
	resp, err := c.request(ctx, http.MethodGet, serverEndpointPath+id+"/", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var server Server
	if err := json.NewDecoder(resp.Body).Decode(&server); err != nil {
		return nil, fmt.Errorf("failed to decode server response: %w", err)
	}
	return &server, nil
}

func (c *APIClient) DeleteServer(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodDelete, serverEndpointPath+id+"/", nil, http.StatusNoContent, http.StatusAccepted)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) CreateServerVolume(ctx context.Context, serverID string, payload VolumeCreateRequest) (*Volume, error) {
	path := fmt.Sprintf("%s%s/volumes/", serverEndpointPath, serverID)
	resp, err := c.request(ctx, http.MethodPost, path, payload, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var volume Volume
	if err := json.NewDecoder(resp.Body).Decode(&volume); err != nil {
		return nil, fmt.Errorf("failed to decode volume response: %w", err)
	}
	return &volume, nil
}

func (c *APIClient) UpdateVolume(ctx context.Context, id string, payload VolumeUpdateRequest) (*Volume, error) {
	resp, err := c.request(ctx, http.MethodPatch, volumeEndpointPath+id+"/", payload, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var volume Volume
	if err := json.NewDecoder(resp.Body).Decode(&volume); err != nil {
		return nil, fmt.Errorf("failed to decode volume response: %w", err)
	}
	return &volume, nil
}

func (c *APIClient) GetVolume(ctx context.Context, id string) (*Volume, error) {
	resp, err := c.request(ctx, http.MethodGet, volumeEndpointPath+id+"/", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var volume Volume
	if err := json.NewDecoder(resp.Body).Decode(&volume); err != nil {
		return nil, fmt.Errorf("failed to decode volume response: %w", err)
	}
	return &volume, nil
}

func (c *APIClient) DetachVolume(ctx context.Context, id, alias string) error {
	body := map[string]string{}
	if alias != "" {
		body["alias"] = alias
	}
	if len(body) == 0 {
		body = nil
	}

	resp, err := c.request(ctx, http.MethodPost, volumeEndpointPath+id+"/detach/", body, http.StatusOK, http.StatusAccepted, http.StatusNoContent)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) DeleteVolume(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodDelete, volumeEndpointPath+id+"/", nil, http.StatusNoContent, http.StatusAccepted)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) WaitForServerReady(ctx context.Context, id string, interval time.Duration) (*Server, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		server, err := c.GetServer(ctx, id)
		if err != nil {
			if errors.Is(err, errNotFound) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-ticker.C:
					continue
				}
			}
			return nil, err
		}

		if server.IsReady() {
			return server, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *APIClient) WaitForServerDeletion(ctx context.Context, id string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		_, err := c.GetServer(ctx, id)
		if err != nil {
			if errors.Is(err, errNotFound) {
				return nil
			}
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *APIClient) WaitForVolumeDetached(ctx context.Context, id string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		volume, err := c.GetVolume(ctx, id)
		if err != nil {
			if errors.Is(err, errNotFound) {
				return nil
			}
			return err
		}

		if !volume.Attached {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *APIClient) WaitForVolumeDeletion(ctx context.Context, id string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		_, err := c.GetVolume(ctx, id)
		if err != nil {
			if errors.Is(err, errNotFound) {
				return nil
			}
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *APIClient) DetachIPv4(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodPost, ipv4EndpointPath+id+"/detach/", map[string]any{}, http.StatusOK)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) DeleteIPv4(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodDelete, ipv4EndpointPath+id+"/", nil, http.StatusNoContent)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) DetachIPv6(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodPost, ipv6EndpointPath+id+"/detach/", map[string]any{}, http.StatusOK)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) DeleteIPv6(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodDelete, ipv6EndpointPath+id+"/", nil, http.StatusNoContent)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func formatAny(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func (c *APIClient) ListImages(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, imagesEndpointPath)
}

func (c *APIClient) ListPackages(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, packagesEndpointPath)
}

func (c *APIClient) GetProfile(ctx context.Context) (map[string]any, error) {
	resp, err := c.request(ctx, http.MethodGet, profileEndpointPath, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var profile map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to decode profile response: %w", err)
	}
	return profile, nil
}

func (c *APIClient) ListVolumeProducts(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, volumeProductsEndpoint)
}

func (c *APIClient) ListVolumes(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, volumeEndpointPath)
}

func (c *APIClient) ListSSHKeys(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, sshKeysEndpointPath)
}

func (c *APIClient) ListFirewalls(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, firewallEndpointPath)
}

func (c *APIClient) ListIPs(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, ipv4EndpointPath)
}

func (c *APIClient) ListIPv6(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, ipv6EndpointPath)
}

func (c *APIClient) ListPublicInterfaces(ctx context.Context) ([]map[string]any, error) {
	resp, err := c.request(ctx, http.MethodGet, publicInterfaceEndpointPath, nil, http.StatusOK)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode public interface response: %w", err)
	}

	switch v := payload.(type) {
	case []any:
		return convertToMapSlice(v), nil
	case map[string]any:
		if results, ok := v["results"].([]any); ok {
			return convertToMapSlice(results), nil
		}
		return convertToMapSliceFromAny(v), nil
	default:
		return []map[string]any{}, nil
	}
}

func (c *APIClient) ListServers(ctx context.Context) ([]map[string]any, error) {
	return c.listCatalog(ctx, serverEndpointPath)
}

func (c *APIClient) CreateFirewallRulesSet(ctx context.Context, name string) (map[string]any, error) {
	body := map[string]string{"name": name}
	resp, err := c.request(ctx, http.MethodPost, firewallEndpointPath, body, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode firewall response: %w", err)
	}
	return payload, nil
}

func (c *APIClient) AddFirewallRule(ctx context.Context, firewallID string, rule map[string]any) error {
	if len(rule) == 0 {
		return nil
	}
	path := fmt.Sprintf("%s%s/rules/", firewallEndpointPath, firewallID)
	resp, err := c.request(ctx, http.MethodPost, path, rule, http.StatusCreated, http.StatusOK)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) DeleteFirewallRulesSet(ctx context.Context, firewallID string) error {
	path := fmt.Sprintf("%s%s/%s", firewallEndpointPath, firewallID, "")
	resp, err := c.request(ctx, http.MethodDelete, path, nil, http.StatusNoContent, http.StatusAccepted, http.StatusOK)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *APIClient) FindFirewall(ctx context.Context, identifier string) (map[string]any, error) {
	firewalls, err := c.ListFirewalls(ctx)
	if err != nil {
		return nil, err
	}
	for _, fw := range firewalls {
		name := stringFromAny(fw["name"])
		slug := stringFromAny(fw["slug"])
		if strings.EqualFold(name, identifier) || strings.EqualFold(slug, identifier) {
			return fw, nil
		}
	}
	return nil, errNotFound
}

func (c *APIClient) listCatalog(ctx context.Context, path string) ([]map[string]any, error) {
	resp, err := c.request(ctx, http.MethodGet, path, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode catalog response: %w", err)
	}

	switch v := payload.(type) {
	case map[string]any:
		if results, ok := v["results"].([]any); ok {
			return convertToMapSlice(results), nil
		}
		return convertToMapSliceFromAny(v), nil
	case []any:
		return convertToMapSlice(v), nil
	default:
		return []map[string]any{}, nil
	}
}

func convertToMapSlice(items []any) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(map[string]any); ok {
			result = append(result, obj)
		}
	}
	return result
}

func convertToMapSliceFromAny(value any) []map[string]any {
	switch v := value.(type) {
	case []any:
		return convertToMapSlice(v)
	case map[string]any:
		return []map[string]any{v}
	default:
		return []map[string]any{}
	}
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case fmt.Stringer:
		return v.String()
	case int, int8, int16, int32, int64, uint, uint64, float32, float64:
		return fmt.Sprintf("%v", v)
	default:
		return ""
	}
}
