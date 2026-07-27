package externalaccess

type tunnelHello struct {
	InstanceID string `json:"instanceId"`
}

type tunnelEnvelope struct {
	Type     string           `json:"type"`
	Request  *tunnelRequest   `json:"request,omitempty"`
	Response *tunnelResponse  `json:"response,omitempty"`
	Error    *tunnelErrorBody `json:"error,omitempty"`
}

type tunnelRequest struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

type tunnelResponse struct {
	ID      string              `json:"id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

type tunnelErrorBody struct {
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

type TunnelHelloAlias = tunnelHello
type TunnelEnvelopeAlias = tunnelEnvelope
type TunnelRequestAlias = tunnelRequest
type TunnelResponseAlias = tunnelResponse
type TunnelErrorAlias = tunnelErrorBody
