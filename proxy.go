package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type Proxy struct {
	config     *Config
	httpClient *http.Client
	zmqProxy   *ZMQProxy
}

func NewProxy(config *Config) *Proxy {
	p := &Proxy{
		config: config,
		httpClient: &http.Client{
			Timeout: config.GetUpstreamTimeout(),
		},
	}
	if config.ZMQ.Enabled {
		p.zmqProxy = NewZMQProxy(config)
	}
	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check proxy authentication if enabled
	if p.config.ProxyAuth.Enabled {
		if !p.checkProxyAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="juno-proxy"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.sendError(w, nil, -32700, "Parse error: failed to read request body")
		return
	}
	defer r.Body.Close()

	// Try to parse as batch request first
	var requests []JSONRPCRequest
	if err := json.Unmarshal(body, &requests); err != nil {
		// Try single request
		var singleReq JSONRPCRequest
		if err := json.Unmarshal(body, &singleReq); err != nil {
			p.sendError(w, nil, -32700, "Parse error: invalid JSON")
			return
		}
		requests = []JSONRPCRequest{singleReq}
	}

	// Check all methods are allowed
	for _, req := range requests {
		if !p.config.IsMethodAllowed(req.Method) {
			log.Printf("Blocked method: %s", req.Method)
			p.sendError(w, req.ID, -32601, fmt.Sprintf("Method not allowed: %s", req.Method))
			return
		}
	}

	// Forward to upstream
	p.forwardRequest(w, body)
}

func (p *Proxy) checkProxyAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}

	if !strings.HasPrefix(auth, "Basic ") {
		return false
	}

	decoded, err := base64.StdEncoding.DecodeString(auth[6:])
	if err != nil {
		return false
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return false
	}

	usernameMatch := subtle.ConstantTimeCompare([]byte(parts[0]), []byte(p.config.ProxyAuth.Username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(parts[1]), []byte(p.config.ProxyAuth.Password)) == 1
	return usernameMatch && passwordMatch
}

func (p *Proxy) forwardRequest(w http.ResponseWriter, body []byte) {
	req, err := http.NewRequest(http.MethodPost, p.config.Upstream.URL, bytes.NewReader(body))
	if err != nil {
		p.sendError(w, nil, -32603, "Internal error: failed to create upstream request")
		return
	}

	req.Header.Set("Content-Type", "application/json")

	// Add upstream authentication
	if p.config.Upstream.Username != "" {
		auth := base64.StdEncoding.EncodeToString(
			[]byte(p.config.Upstream.Username + ":" + p.config.Upstream.Password),
		)
		req.Header.Set("Authorization", "Basic "+auth)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		log.Printf("Upstream error: %v", err)
		p.sendError(w, nil, -32603, "Internal error: upstream connection failed")
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("Error copying response body: %v", err)
	}
}

func (p *Proxy) sendError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	if id == nil {
		id = json.RawMessage("null")
	}

	response := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (p *Proxy) Stop() {
	if p.zmqProxy != nil {
		p.zmqProxy.Stop()
	}
}
