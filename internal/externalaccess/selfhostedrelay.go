package externalaccess

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type SelfHostedRelayOptions struct {
	BaseURL      string
	TunnelURL    string
	SharedSecret string
	InstanceID   string
	Now          func() time.Time
}

type SelfHostedRelayProvider struct {
	now          func() time.Time
	baseURL      string
	tunnelURL    string
	sharedSecret string
	instanceID   string

	mu             sync.Mutex
	publicBase     PublicBase
	boundTargetURL string
	lastError      string
	cancel         context.CancelFunc
	running        bool
}

func NewSelfHostedRelayProvider(opts SelfHostedRelayOptions) *SelfHostedRelayProvider {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	tunnelURL := strings.TrimSpace(opts.TunnelURL)
	if tunnelURL == "" && baseURL != "" {
		tunnelURL = deriveTunnelURL(baseURL)
	}
	instanceID := strings.TrimSpace(opts.InstanceID)
	if instanceID == "" {
		instanceID = "local-" + randomRelayToken(8)
	}
	return &SelfHostedRelayProvider{
		now:          now,
		baseURL:      baseURL,
		tunnelURL:    tunnelURL,
		sharedSecret: strings.TrimSpace(opts.SharedSecret),
		instanceID:   instanceID,
	}
}

func (p *SelfHostedRelayProvider) Kind() string { return "selfhostedrelay" }

func (p *SelfHostedRelayProvider) Snapshot() ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProviderStatus{
		Kind:      p.Kind(),
		BaseURL:   p.publicBase.BaseURL,
		StartedAt: p.publicBase.StartedAt,
		Ready:     p.running,
		LastError: p.lastError,
	}
}

func (p *SelfHostedRelayProvider) EnsurePublicBase(ctx context.Context, localListenerURL string) (PublicBase, error) {
	localListenerURL = strings.TrimSpace(localListenerURL)
	if localListenerURL == "" {
		return PublicBase{}, fmt.Errorf("self-hosted relay target is required")
	}
	if strings.TrimSpace(p.baseURL) == "" {
		return PublicBase{}, fmt.Errorf("self-hosted relay base URL is required")
	}
	if strings.TrimSpace(p.tunnelURL) == "" {
		return PublicBase{}, fmt.Errorf("self-hosted relay tunnel URL is required")
	}
	if strings.TrimSpace(p.sharedSecret) == "" {
		return PublicBase{}, fmt.Errorf("self-hosted relay shared secret is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running && p.boundTargetURL == localListenerURL && strings.TrimSpace(p.publicBase.BaseURL) != "" {
		return p.publicBase, nil
	}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.boundTargetURL = localListenerURL
	p.publicBase = PublicBase{
		BaseURL:   strings.TrimRight(p.baseURL, "/") + "/t/" + p.instanceID,
		StartedAt: p.now().UTC(),
	}
	p.running = true
	p.lastError = ""
	go p.run(runCtx, localListenerURL)
	return p.publicBase, nil
}

func (p *SelfHostedRelayProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	p.cancel = nil
	p.running = false
	p.boundTargetURL = ""
	p.publicBase = PublicBase{}
	return nil
}

func (p *SelfHostedRelayProvider) run(ctx context.Context, localListenerURL string) {
	backoff := time.Second
	for {
		err := p.runOnce(ctx, localListenerURL)
		if ctx.Err() != nil {
			return
		}
		p.recordError(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (p *SelfHostedRelayProvider) runOnce(ctx context.Context, localListenerURL string) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.sharedSecret)
	header.Set("X-Codex-Relay-Instance", p.instanceID)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, p.tunnelURL, header)
	if err != nil {
		return fmt.Errorf("dial self-hosted relay: %w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(32 << 20)
	if err := conn.WriteJSON(tunnelHello{InstanceID: p.instanceID}); err != nil {
		return fmt.Errorf("write relay hello: %w", err)
	}

	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	publicPathPrefix := selfHostedRelayPublicPath(p.baseURL, p.instanceID)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var envelope tunnelEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return fmt.Errorf("read relay request: %w", err)
		}
		if envelope.Type != "request" || envelope.Request == nil {
			continue
		}
		response := handleRelayRequest(client, localListenerURL, envelope.Request)
		RewriteTunnelResponsePublicPath(response, publicPathPrefix)
		if err := conn.WriteJSON(tunnelEnvelope{Type: "response", Response: response}); err != nil {
			return fmt.Errorf("write relay response: %w", err)
		}
	}
}

func handleRelayRequest(client *http.Client, localListenerURL string, request *tunnelRequest) *tunnelResponse {
	targetURL, err := joinRelayTarget(localListenerURL, request.Path)
	if err != nil {
		return &tunnelResponse{
			ID:     request.ID,
			Status: http.StatusBadGateway,
			Headers: map[string][]string{
				"Content-Type": {"text/plain; charset=utf-8"},
			},
			Body: []byte(err.Error()),
		}
	}
	httpReq, err := http.NewRequestWithContext(context.Background(), request.Method, targetURL, bytes.NewReader(request.Body))
	if err != nil {
		return &tunnelResponse{
			ID:     request.ID,
			Status: http.StatusBadGateway,
			Headers: map[string][]string{
				"Content-Type": {"text/plain; charset=utf-8"},
			},
			Body: []byte(err.Error()),
		}
	}
	for key, values := range request.Headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "host" || lower == "content-length" {
			continue
		}
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return &tunnelResponse{
			ID:     request.ID,
			Status: http.StatusBadGateway,
			Headers: map[string][]string{
				"Content-Type": {"text/plain; charset=utf-8"},
			},
			Body: []byte(err.Error()),
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	headers := map[string][]string{}
	for key, values := range resp.Header {
		copied := make([]string, len(values))
		copy(copied, values)
		headers[key] = copied
	}
	return &tunnelResponse{
		ID:      request.ID,
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    body,
	}
}

func joinRelayTarget(baseURL, requestPath string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(requestPath) == "" {
		requestPath = "/"
	}
	relayPath := requestPath
	relayQuery := ""
	if idx := strings.IndexByte(relayPath, '?'); idx >= 0 {
		relayQuery = relayPath[idx+1:]
		relayPath = relayPath[:idx]
	}
	parsed.Path = joinRelayURLPath(parsed.Path, relayPath)
	parsed.RawQuery = relayQuery
	return parsed.String(), nil
}

func joinRelayURLPath(basePath, requestPath string) string {
	basePath = "/" + strings.TrimPrefix(basePath, "/")
	if requestPath == "/" {
		return basePath
	}
	joined := path.Join(basePath, requestPath)
	if strings.HasSuffix(requestPath, "/") && !strings.HasSuffix(joined, "/") {
		return joined + "/"
	}
	return joined
}

func deriveTunnelURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = joinRelayURLPath(parsed.Path, "/ws/tunnel")
	parsed.RawQuery = ""
	return parsed.String()
}

func selfHostedRelayPublicPath(baseURL, instanceID string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return joinRelayURLPath(parsed.Path, "/t/"+strings.TrimSpace(instanceID))
}

func (p *SelfHostedRelayProvider) recordError(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err.Error()
}

func randomRelayToken(byteLen int) string {
	raw := make([]byte, byteLen)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(raw), "=")
}
