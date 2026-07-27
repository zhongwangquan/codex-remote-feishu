package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kxn/codex-remote-feishu/internal/externalaccess"
)

type relayServer struct {
	sharedSecret string
	upgrader     websocket.Upgrader

	mu      sync.RWMutex
	clients map[string]*relayClient
}

type relayClient struct {
	instanceID string
	conn       *websocket.Conn
	writeMu    sync.Mutex

	mu      sync.Mutex
	pending map[string]chan *externalaccess.TunnelResponseAlias
}

func main() {
	listenAddr := envOr("PREVIEW_RELAY_LISTEN_ADDR", ":8080")
	sharedSecret := strings.TrimSpace(os.Getenv("PREVIEW_RELAY_SHARED_SECRET"))
	if sharedSecret == "" {
		log.Fatal("PREVIEW_RELAY_SHARED_SECRET is required")
	}
	basePath := normalizeBasePath(os.Getenv("PREVIEW_RELAY_BASE_PATH"))

	server := &relayServer{
		sharedSecret: sharedSecret,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
		clients: map[string]*relayClient{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+joinPublicPath(basePath, "/healthz"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET "+joinPublicPath(basePath, "/ws/tunnel"), server.handleTunnel)
	mux.HandleFunc("/", server.handlePublicRequest)

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("preview relay listening on %s", listenAddr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (s *relayServer) handleTunnel(w http.ResponseWriter, r *http.Request) {
	if !authorized(r.Header.Get("Authorization"), s.sharedSecret) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var hello externalaccess.TunnelHelloAlias
	if err := conn.ReadJSON(&hello); err != nil {
		return
	}
	instanceID := strings.TrimSpace(hello.InstanceID)
	if instanceID == "" {
		return
	}

	client := &relayClient{
		instanceID: instanceID,
		conn:       conn,
		pending:    map[string]chan *externalaccess.TunnelResponseAlias{},
	}
	s.registerClient(client)
	defer s.unregisterClient(client)

	for {
		var envelope externalaccess.TunnelEnvelopeAlias
		if err := conn.ReadJSON(&envelope); err != nil {
			return
		}
		if envelope.Type != "response" || envelope.Response == nil {
			continue
		}
		client.mu.Lock()
		waiter := client.pending[envelope.Response.ID]
		if waiter != nil {
			delete(client.pending, envelope.Response.ID)
		}
		client.mu.Unlock()
		if waiter != nil {
			waiter <- envelope.Response
		}
	}
}

func (s *relayServer) handlePublicRequest(w http.ResponseWriter, r *http.Request) {
	basePath := normalizeBasePath(os.Getenv("PREVIEW_RELAY_BASE_PATH"))
	if isTunnelControlPath(basePath, r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	instanceID, relayPath, ok := splitRelayPath(basePath, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	client := s.lookupClient(instanceID)
	if client == nil {
		http.Error(w, "preview tunnel offline", http.StatusServiceUnavailable)
		return
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "websocket relay is not enabled", http.StatusNotImplemented)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	requestID := randomID()
	waiter := make(chan *externalaccess.TunnelResponseAlias, 1)
	client.mu.Lock()
	client.pending[requestID] = waiter
	client.mu.Unlock()

	request := &externalaccess.TunnelRequestAlias{
		ID:      requestID,
		Method:  r.Method,
		Path:    withQuery(relayPath, r.URL.RawQuery),
		Headers: cloneHeaders(r.Header),
		Body:    body,
	}
	if err := client.writeEnvelope(externalaccess.TunnelEnvelopeAlias{Type: "request", Request: request}); err != nil {
		client.mu.Lock()
		delete(client.pending, requestID)
		client.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	select {
	case response := <-waiter:
		rewriteExternalAccessHeaders(response, basePath, instanceID)
		writeRelayResponse(w, response)
	case <-time.After(90 * time.Second):
		client.mu.Lock()
		delete(client.pending, requestID)
		client.mu.Unlock()
		http.Error(w, "preview relay timeout", http.StatusGatewayTimeout)
	case <-r.Context().Done():
		client.mu.Lock()
		delete(client.pending, requestID)
		client.mu.Unlock()
		http.Error(w, "request canceled", http.StatusRequestTimeout)
	}
}

func (s *relayServer) registerClient(client *relayClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.clients[client.instanceID]; existing != nil && existing.conn != nil {
		_ = existing.conn.Close()
	}
	s.clients[client.instanceID] = client
}

func (s *relayServer) unregisterClient(client *relayClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.clients[client.instanceID]; current == client {
		delete(s.clients, client.instanceID)
	}
}

func (s *relayServer) lookupClient(instanceID string) *relayClient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clients[instanceID]
}

func (c *relayClient) writeEnvelope(value externalaccess.TunnelEnvelopeAlias) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(value)
}

func writeRelayResponse(w http.ResponseWriter, response *externalaccess.TunnelResponseAlias) {
	for key, values := range response.Headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "content-length" || lower == "transfer-encoding" || lower == "connection" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := response.Status
	if status <= 0 {
		status = http.StatusBadGateway
	}
	w.WriteHeader(status)
	_, _ = w.Write(response.Body)
}

func rewriteExternalAccessHeaders(response *externalaccess.TunnelResponseAlias, basePath, instanceID string) {
	externalPrefix := joinPublicPath(basePath, "/t/"+instanceID)
	externalaccess.RewriteTunnelResponsePublicPath(response, externalPrefix)
}

func splitRelayPath(basePath, rawPath string) (string, string, bool) {
	if basePath != "" {
		if rawPath == basePath {
			rawPath = "/"
		} else if strings.HasPrefix(rawPath, basePath+"/") {
			rawPath = strings.TrimPrefix(rawPath, basePath)
		}
	}
	trimmed := strings.TrimPrefix(rawPath, "/")
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) < 2 || parts[0] != "t" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	if len(parts) == 2 {
		return parts[1], "/", true
	}
	return parts[1], "/" + parts[2], true
}

func cloneHeaders(header http.Header) map[string][]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string][]string, len(header))
	for key, values := range header {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func withQuery(pathValue, rawQuery string) string {
	if rawQuery == "" {
		return pathValue
	}
	return pathValue + "?" + rawQuery
}

func isTunnelControlPath(basePath, requestPath string) bool {
	return requestPath == joinPublicPath(basePath, "/healthz") || requestPath == joinPublicPath(basePath, "/ws/tunnel")
}

func authorized(headerValue, sharedSecret string) bool {
	return strings.TrimSpace(headerValue) == "Bearer "+sharedSecret
}

func envOr(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func randomID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(buf), "=")
}

func normalizeBasePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	return "/" + strings.Trim(strings.TrimPrefix(value, "/"), "/")
}

func joinPublicPath(basePath, suffix string) string {
	basePath = normalizeBasePath(basePath)
	if basePath == "" {
		if suffix == "" {
			return "/"
		}
		if strings.HasPrefix(suffix, "/") {
			return suffix
		}
		return "/" + suffix
	}
	if suffix == "" || suffix == "/" {
		return basePath
	}
	return path.Join(basePath, suffix)
}
