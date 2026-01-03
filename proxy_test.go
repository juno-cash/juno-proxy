package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testConfig returns a minimal valid config for testing
func testConfig() *Config {
	return &Config{
		Listen:         ":8080",
		Upstream:       Upstream{URL: "http://localhost:8232", Timeout: "30s"},
		AllowedMethods: []string{"getinfo", "getblock", "getblocktemplate"},
	}
}

// testConfigWithAuth returns a config with proxy auth enabled
func testConfigWithAuth(username, password string) *Config {
	config := testConfig()
	config.ProxyAuth = ProxyAuth{
		Enabled:  true,
		Username: username,
		Password: password,
	}
	return config
}

func TestNewProxy(t *testing.T) {
	t.Run("without ZMQ", func(t *testing.T) {
		config := testConfig()
		proxy := NewProxy(config)
		if proxy == nil {
			t.Fatal("NewProxy() returned nil")
		}
		if proxy.zmqProxy != nil {
			t.Error("zmqProxy should be nil when ZMQ is disabled")
		}
		if proxy.httpClient == nil {
			t.Error("httpClient should not be nil")
		}
	})

	t.Run("with ZMQ enabled", func(t *testing.T) {
		config := testConfig()
		config.ZMQ = ZMQConfig{
			Enabled:     true,
			UpstreamURL: "tcp://127.0.0.1:28332",
			Listen:      "tcp://0.0.0.0:28333",
		}
		proxy := NewProxy(config)
		if proxy.zmqProxy == nil {
			t.Error("zmqProxy should not be nil when ZMQ is enabled")
		}
	})
}

func TestServeHTTP_MethodNotAllowed(t *testing.T) {
	config := testConfig()
	proxy := NewProxy(config)

	methods := []string{"GET", "PUT", "DELETE", "PATCH", "OPTIONS"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/", nil)
			rr := httptest.NewRecorder()
			proxy.ServeHTTP(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestServeHTTP_ProxyAuth(t *testing.T) {
	t.Run("auth disabled allows requests", func(t *testing.T) {
		// Start mock upstream
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
		}))
		defer upstream.Close()

		config := testConfig()
		config.Upstream.URL = upstream.URL
		proxy := NewProxy(config)

		body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
	})

	t.Run("auth enabled rejects missing credentials", func(t *testing.T) {
		config := testConfigWithAuth("admin", "secret")
		proxy := NewProxy(config)

		body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
		if rr.Header().Get("WWW-Authenticate") == "" {
			t.Error("WWW-Authenticate header should be set")
		}
	})

	t.Run("auth enabled accepts valid credentials", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
		}))
		defer upstream.Close()

		config := testConfigWithAuth("admin", "secret")
		config.Upstream.URL = upstream.URL
		proxy := NewProxy(config)

		body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
	})

	t.Run("auth enabled rejects wrong credentials", func(t *testing.T) {
		config := testConfigWithAuth("admin", "secret")
		proxy := NewProxy(config)

		body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:wrong")))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}

func TestServeHTTP_JSONParsing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	config := testConfig()
	config.Upstream.URL = upstream.URL
	proxy := NewProxy(config)

	t.Run("valid single request", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
	})

	t.Run("valid batch request", func(t *testing.T) {
		body := `[{"jsonrpc":"2.0","id":1,"method":"getinfo"},{"jsonrpc":"2.0","id":2,"method":"getblock"}]`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		body := `{invalid json`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d (JSON-RPC errors use 200)", rr.Code, http.StatusOK)
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if resp.Error == nil {
			t.Error("expected error in response")
		}
		if resp.Error != nil && resp.Error.Code != -32700 {
			t.Errorf("error code = %d, want -32700 (parse error)", resp.Error.Code)
		}
	})
}

func TestServeHTTP_MethodFiltering(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	config := testConfig()
	config.Upstream.URL = upstream.URL
	proxy := NewProxy(config)

	t.Run("allowed method passes", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		var resp JSONRPCResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp.Error != nil {
			t.Errorf("unexpected error: %v", resp.Error)
		}
	})

	t.Run("blocked method rejected", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":1,"method":"sendtoaddress"}`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		var resp JSONRPCResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp.Error == nil {
			t.Error("expected error for blocked method")
		}
		if resp.Error != nil && resp.Error.Code != -32601 {
			t.Errorf("error code = %d, want -32601", resp.Error.Code)
		}
	})

	t.Run("batch with blocked method rejected", func(t *testing.T) {
		body := `[{"jsonrpc":"2.0","id":1,"method":"getinfo"},{"jsonrpc":"2.0","id":2,"method":"sendtoaddress"}]`
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rr := httptest.NewRecorder()

		proxy.ServeHTTP(rr, req)

		var resp JSONRPCResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp.Error == nil {
			t.Error("expected error for batch with blocked method")
		}
	})
}

func TestCheckProxyAuth(t *testing.T) {
	config := testConfigWithAuth("testuser", "testpass")
	proxy := NewProxy(config)

	tests := []struct {
		name     string
		authHdr  string
		expected bool
	}{
		{"valid credentials", "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:testpass")), true},
		{"wrong password", "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:wrongpass")), false},
		{"wrong username", "Basic " + base64.StdEncoding.EncodeToString([]byte("wronguser:testpass")), false},
		{"empty header", "", false},
		{"invalid base64", "Basic !!!notbase64!!!", false},
		{"no colon in credentials", "Basic " + base64.StdEncoding.EncodeToString([]byte("nocredentials")), false},
		{"Bearer instead of Basic", "Bearer token123", false},
		{"empty username", "Basic " + base64.StdEncoding.EncodeToString([]byte(":testpass")), false},
		{"empty password", "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", nil)
			if tt.authHdr != "" {
				req.Header.Set("Authorization", tt.authHdr)
			}
			got := proxy.checkProxyAuth(req)
			if got != tt.expected {
				t.Errorf("checkProxyAuth() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSendError(t *testing.T) {
	config := testConfig()
	proxy := NewProxy(config)

	t.Run("basic error response", func(t *testing.T) {
		rr := httptest.NewRecorder()
		proxy.sendError(rr, json.RawMessage(`1`), -32600, "Invalid Request")

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if resp.JSONRPC != "2.0" {
			t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
		}
		if string(resp.ID) != "1" {
			t.Errorf("id = %s, want 1", resp.ID)
		}
		if resp.Error == nil {
			t.Fatal("error should not be nil")
		}
		if resp.Error.Code != -32600 {
			t.Errorf("error.code = %d, want -32600", resp.Error.Code)
		}
		if resp.Error.Message != "Invalid Request" {
			t.Errorf("error.message = %q, want %q", resp.Error.Message, "Invalid Request")
		}
	})

	t.Run("null ID", func(t *testing.T) {
		rr := httptest.NewRecorder()
		proxy.sendError(rr, nil, -32700, "Parse error")

		var resp JSONRPCResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if string(resp.ID) != "null" {
			t.Errorf("id = %s, want null", resp.ID)
		}
	})
}

func TestFullRPCFlow(t *testing.T) {
	// Create mock upstream that echoes the method
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req JSONRPCRequest
		json.Unmarshal(body, &req)

		resp := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`"method was: ` + req.Method + `"`),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	config := testConfig()
	config.Upstream.URL = upstream.URL
	proxy := NewProxy(config)

	body := `{"jsonrpc":"2.0","id":42,"method":"getinfo"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if string(resp.ID) != "42" {
		t.Errorf("id = %s, want 42", resp.ID)
	}
	result := string(resp.Result)
	if !strings.Contains(result, "getinfo") {
		t.Errorf("result = %s, want to contain 'getinfo'", result)
	}
}

func TestUpstreamAuth(t *testing.T) {
	var receivedAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	config := testConfig()
	config.Upstream.URL = upstream.URL
	config.Upstream.Username = "rpcuser"
	config.Upstream.Password = "rpcpass"
	proxy := NewProxy(config)

	body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("rpcuser:rpcpass"))
	if receivedAuth != expectedAuth {
		t.Errorf("upstream auth = %q, want %q", receivedAuth, expectedAuth)
	}
}

func TestUpstreamTimeout(t *testing.T) {
	// Create slow upstream
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	config := testConfig()
	config.Upstream.URL = upstream.URL
	config.Upstream.Timeout = "50ms" // shorter than upstream delay
	proxy := NewProxy(config)

	body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	var resp JSONRPCResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error == nil {
		t.Error("expected timeout error")
	}
	if resp.Error != nil && resp.Error.Code != -32603 {
		t.Errorf("error code = %d, want -32603 (internal error)", resp.Error.Code)
	}
}

func TestProxyStop(t *testing.T) {
	t.Run("stop with nil zmqProxy", func(t *testing.T) {
		config := testConfig()
		proxy := NewProxy(config)
		// Should not panic
		proxy.Stop()
	})
}

func TestResponseHeaderCopying(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Header", "custom-value")
		w.Header().Add("X-Multi-Header", "value1")
		w.Header().Add("X-Multi-Header", "value2")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	config := testConfig()
	config.Upstream.URL = upstream.URL
	proxy := NewProxy(config)

	body := `{"jsonrpc":"2.0","id":1,"method":"getinfo"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	if rr.Header().Get("X-Custom-Header") != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", rr.Header().Get("X-Custom-Header"), "custom-value")
	}
	multiValues := rr.Header().Values("X-Multi-Header")
	if len(multiValues) != 2 {
		t.Errorf("X-Multi-Header values = %d, want 2", len(multiValues))
	}
}

func BenchmarkServeHTTP(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	config := testConfig()
	config.Upstream.URL = upstream.URL
	proxy := NewProxy(config)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"getinfo"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		proxy.ServeHTTP(rr, req)
	}
}
