package main

import (
	"sync"
	"testing"
	"time"

	zmq "github.com/pebbe/zmq4"
)

// testConfigWithZMQ returns a config with ZMQ enabled for testing
func testConfigWithZMQ(upstreamURL, listenURL string) *Config {
	return &Config{
		Listen:         ":8080",
		Upstream:       Upstream{URL: "http://localhost:8232"},
		AllowedMethods: []string{"getinfo"},
		ZMQ: ZMQConfig{
			Enabled:     true,
			UpstreamURL: upstreamURL,
			Listen:      listenURL,
			Topic:       "hashblock",
		},
	}
}

func TestNewZMQProxy(t *testing.T) {
	config := testConfigWithZMQ("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333")
	proxy := NewZMQProxy(config)

	if proxy == nil {
		t.Fatal("NewZMQProxy() returned nil")
	}
	if proxy.config != config {
		t.Error("config not set correctly")
	}
	if proxy.stoppedCh == nil {
		t.Error("stoppedCh should be initialized")
	}
	if proxy.running {
		t.Error("running should be false initially")
	}
}

func TestZMQProxyDoubleStop(t *testing.T) {
	config := testConfigWithZMQ("tcp://127.0.0.1:28332", "tcp://127.0.0.1:28333")
	proxy := NewZMQProxy(config)

	// Should not panic when stopping a non-started proxy
	proxy.Stop()
	proxy.Stop() // Double stop should be safe
}

func TestZMQProxyStartStop(t *testing.T) {
	// Use unique ports to avoid conflicts with other tests
	config := testConfigWithZMQ("tcp://127.0.0.1:39332", "tcp://127.0.0.1:39333")
	proxy := NewZMQProxy(config)

	// Start the proxy
	err := proxy.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Verify it's running
	proxy.mu.Lock()
	running := proxy.running
	proxy.mu.Unlock()
	if !running {
		t.Error("proxy should be running after Start()")
	}

	// Stop the proxy
	proxy.Stop()

	// Verify it's stopped
	proxy.mu.Lock()
	running = proxy.running
	proxy.mu.Unlock()
	if running {
		t.Error("proxy should not be running after Stop()")
	}
}

func TestZMQProxyDoubleStart(t *testing.T) {
	config := testConfigWithZMQ("tcp://127.0.0.1:39334", "tcp://127.0.0.1:39335")
	proxy := NewZMQProxy(config)

	err := proxy.Start()
	if err != nil {
		t.Fatalf("First Start() error = %v", err)
	}
	defer proxy.Stop()

	// Second start should be a no-op (idempotent)
	err = proxy.Start()
	if err != nil {
		t.Errorf("Second Start() should not error, got: %v", err)
	}
}

func TestZMQProxyConnectError(t *testing.T) {
	// Test with invalid upstream URL format
	config := testConfigWithZMQ("invalid://url", "tcp://127.0.0.1:39336")
	proxy := NewZMQProxy(config)

	err := proxy.Start()
	if err == nil {
		proxy.Stop()
		t.Error("Start() should fail with invalid upstream URL")
	}
}

func TestZMQProxyBindError(t *testing.T) {
	// First proxy binds to port
	config1 := testConfigWithZMQ("tcp://127.0.0.1:39337", "tcp://127.0.0.1:39338")
	proxy1 := NewZMQProxy(config1)
	err := proxy1.Start()
	if err != nil {
		t.Fatalf("First proxy Start() error = %v", err)
	}
	defer proxy1.Stop()

	// Second proxy tries to bind to same port - should fail
	config2 := testConfigWithZMQ("tcp://127.0.0.1:39339", "tcp://127.0.0.1:39338")
	proxy2 := NewZMQProxy(config2)
	err = proxy2.Start()
	if err == nil {
		proxy2.Stop()
		t.Error("Start() should fail when binding to already-used port")
	}
}

func TestZMQProxyForwarding(t *testing.T) {
	// This test requires a full pub/sub setup
	// Create a publisher (simulating upstream)
	pubCtx, err := zmq.NewContext()
	if err != nil {
		t.Fatalf("Failed to create publisher context: %v", err)
	}
	defer pubCtx.Term()

	publisher, err := pubCtx.NewSocket(zmq.PUB)
	if err != nil {
		t.Fatalf("Failed to create publisher socket: %v", err)
	}
	defer publisher.Close()

	err = publisher.Bind("tcp://127.0.0.1:39340")
	if err != nil {
		t.Fatalf("Failed to bind publisher: %v", err)
	}

	// Start the proxy
	config := testConfigWithZMQ("tcp://127.0.0.1:39340", "tcp://127.0.0.1:39341")
	config.ZMQ.Topic = "test"
	proxy := NewZMQProxy(config)
	err = proxy.Start()
	if err != nil {
		t.Fatalf("Proxy Start() error = %v", err)
	}
	defer proxy.Stop()

	// Create a subscriber (simulating downstream client)
	subCtx, err := zmq.NewContext()
	if err != nil {
		t.Fatalf("Failed to create subscriber context: %v", err)
	}
	defer subCtx.Term()

	subscriber, err := subCtx.NewSocket(zmq.SUB)
	if err != nil {
		t.Fatalf("Failed to create subscriber socket: %v", err)
	}
	defer subscriber.Close()

	err = subscriber.Connect("tcp://127.0.0.1:39341")
	if err != nil {
		t.Fatalf("Failed to connect subscriber: %v", err)
	}
	subscriber.SetSubscribe("test")

	// Give sockets time to connect
	time.Sleep(100 * time.Millisecond)

	// Publish a message
	testMessage := "test:hello_world"
	_, err = publisher.Send(testMessage, 0)
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Set receive timeout
	subscriber.SetRcvtimeo(2 * time.Second)

	// Receive the message
	received, err := subscriber.Recv(0)
	if err != nil {
		t.Fatalf("Failed to receive message: %v", err)
	}

	if received != testMessage {
		t.Errorf("Received = %q, want %q", received, testMessage)
	}
}

func TestZMQProxyMultipleSubscribers(t *testing.T) {
	// Create publisher
	pubCtx, err := zmq.NewContext()
	if err != nil {
		t.Fatalf("Failed to create publisher context: %v", err)
	}
	defer pubCtx.Term()

	publisher, err := pubCtx.NewSocket(zmq.PUB)
	if err != nil {
		t.Fatalf("Failed to create publisher socket: %v", err)
	}
	defer publisher.Close()

	err = publisher.Bind("tcp://127.0.0.1:39350")
	if err != nil {
		t.Fatalf("Failed to bind publisher: %v", err)
	}

	// Start proxy
	config := testConfigWithZMQ("tcp://127.0.0.1:39350", "tcp://127.0.0.1:39351")
	config.ZMQ.Topic = "block"
	proxy := NewZMQProxy(config)
	err = proxy.Start()
	if err != nil {
		t.Fatalf("Proxy Start() error = %v", err)
	}
	defer proxy.Stop()

	// Create multiple subscribers
	numSubscribers := 3
	subscribers := make([]*zmq.Socket, numSubscribers)
	contexts := make([]*zmq.Context, numSubscribers)

	for i := 0; i < numSubscribers; i++ {
		ctx, err := zmq.NewContext()
		if err != nil {
			t.Fatalf("Failed to create subscriber context %d: %v", i, err)
		}
		contexts[i] = ctx

		sub, err := ctx.NewSocket(zmq.SUB)
		if err != nil {
			t.Fatalf("Failed to create subscriber %d: %v", i, err)
		}
		subscribers[i] = sub

		err = sub.Connect("tcp://127.0.0.1:39351")
		if err != nil {
			t.Fatalf("Failed to connect subscriber %d: %v", i, err)
		}
		sub.SetSubscribe("block")
		sub.SetRcvtimeo(2 * time.Second)
	}

	defer func() {
		for i := 0; i < numSubscribers; i++ {
			subscribers[i].Close()
			contexts[i].Term()
		}
	}()

	// Give sockets time to connect
	time.Sleep(100 * time.Millisecond)

	// Publish a message
	testMessage := "block:12345"
	_, err = publisher.Send(testMessage, 0)
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// All subscribers should receive the message
	var wg sync.WaitGroup
	errors := make(chan error, numSubscribers)

	for i := 0; i < numSubscribers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			received, err := subscribers[idx].Recv(0)
			if err != nil {
				errors <- err
				return
			}
			if received != testMessage {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Errorf("Subscriber error: %v", err)
		}
	}
}

func TestZMQProxyTopicFiltering(t *testing.T) {
	// Create publisher
	pubCtx, err := zmq.NewContext()
	if err != nil {
		t.Fatalf("Failed to create publisher context: %v", err)
	}
	defer pubCtx.Term()

	publisher, err := pubCtx.NewSocket(zmq.PUB)
	if err != nil {
		t.Fatalf("Failed to create publisher socket: %v", err)
	}
	defer publisher.Close()

	err = publisher.Bind("tcp://127.0.0.1:39360")
	if err != nil {
		t.Fatalf("Failed to bind publisher: %v", err)
	}

	// Start proxy with specific topic
	config := testConfigWithZMQ("tcp://127.0.0.1:39360", "tcp://127.0.0.1:39361")
	config.ZMQ.Topic = "hashblock"
	proxy := NewZMQProxy(config)
	err = proxy.Start()
	if err != nil {
		t.Fatalf("Proxy Start() error = %v", err)
	}
	defer proxy.Stop()

	// Create subscriber
	subCtx, err := zmq.NewContext()
	if err != nil {
		t.Fatalf("Failed to create subscriber context: %v", err)
	}
	defer subCtx.Term()

	subscriber, err := subCtx.NewSocket(zmq.SUB)
	if err != nil {
		t.Fatalf("Failed to create subscriber: %v", err)
	}
	defer subscriber.Close()

	err = subscriber.Connect("tcp://127.0.0.1:39361")
	if err != nil {
		t.Fatalf("Failed to connect subscriber: %v", err)
	}
	subscriber.SetSubscribe("hashblock")
	subscriber.SetRcvtimeo(500 * time.Millisecond)

	// Give sockets time to connect
	time.Sleep(100 * time.Millisecond)

	// Publish messages with different topics
	publisher.Send("hashblock:block1", 0)
	publisher.Send("rawtx:tx1", 0) // Should not be received
	publisher.Send("hashblock:block2", 0)

	// Receive messages
	received := []string{}
	for {
		msg, err := subscriber.Recv(0)
		if err != nil {
			break // Timeout
		}
		received = append(received, msg)
	}

	// Should receive only hashblock messages
	if len(received) != 2 {
		t.Errorf("Received %d messages, want 2", len(received))
	}
	for _, msg := range received {
		if msg != "hashblock:block1" && msg != "hashblock:block2" {
			t.Errorf("Unexpected message: %s", msg)
		}
	}
}

func BenchmarkZMQProxyForwarding(b *testing.B) {
	// Create publisher
	pubCtx, _ := zmq.NewContext()
	defer pubCtx.Term()

	publisher, _ := pubCtx.NewSocket(zmq.PUB)
	defer publisher.Close()
	publisher.Bind("tcp://127.0.0.1:39370")

	// Start proxy
	config := testConfigWithZMQ("tcp://127.0.0.1:39370", "tcp://127.0.0.1:39371")
	config.ZMQ.Topic = "bench"
	proxy := NewZMQProxy(config)
	proxy.Start()
	defer proxy.Stop()

	// Create subscriber
	subCtx, _ := zmq.NewContext()
	defer subCtx.Term()

	subscriber, _ := subCtx.NewSocket(zmq.SUB)
	defer subscriber.Close()
	subscriber.Connect("tcp://127.0.0.1:39371")
	subscriber.SetSubscribe("bench")
	subscriber.SetRcvtimeo(1 * time.Second)

	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		publisher.Send("bench:message", 0)
		subscriber.Recv(0)
	}
}
