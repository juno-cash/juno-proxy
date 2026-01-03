package main

import (
	"log"
	"sync"

	zmq "github.com/pebbe/zmq4"
)

type ZMQProxy struct {
	config    *Config
	running   bool
	mu        sync.Mutex
	ctx       *zmq.Context
	frontend  *zmq.Socket // XSUB - connects to upstream
	backend   *zmq.Socket // XPUB - clients connect here
	stoppedCh chan struct{}
}

func NewZMQProxy(config *Config) *ZMQProxy {
	return &ZMQProxy{
		config:    config,
		stoppedCh: make(chan struct{}),
	}
}

func (z *ZMQProxy) Start() error {
	z.mu.Lock()
	if z.running {
		z.mu.Unlock()
		return nil
	}
	z.running = true
	z.mu.Unlock()

	var err error

	// Create ZMQ context
	z.ctx, err = zmq.NewContext()
	if err != nil {
		return err
	}

	// Create XSUB socket (frontend) - connects to upstream publisher
	z.frontend, err = z.ctx.NewSocket(zmq.XSUB)
	if err != nil {
		z.ctx.Term()
		return err
	}

	// Create XPUB socket (backend) - clients subscribe here
	z.backend, err = z.ctx.NewSocket(zmq.XPUB)
	if err != nil {
		z.frontend.Close()
		z.ctx.Term()
		return err
	}

	// Connect frontend to upstream
	if err := z.frontend.Connect(z.config.ZMQ.UpstreamURL); err != nil {
		z.cleanup()
		z.ctx.Term()
		return err
	}

	// Bind backend for clients
	if err := z.backend.Bind(z.config.ZMQ.Listen); err != nil {
		z.cleanup()
		z.ctx.Term()
		return err
	}

	log.Printf("ZMQ proxy started: %s -> %s (topic: %s)",
		z.config.ZMQ.UpstreamURL, z.config.ZMQ.Listen, z.config.GetZMQTopic())

	// Start the proxy in a goroutine
	go z.run()

	return nil
}

func (z *ZMQProxy) run() {
	defer close(z.stoppedCh)
	defer z.cleanup()

	// Subscribe to the configured topic on the frontend
	// XSUB subscription message format: 0x01 followed by topic
	topic := z.config.GetZMQTopic()
	subscribeMsg := append([]byte{0x01}, []byte(topic)...)
	z.frontend.SendBytes(subscribeMsg, 0)

	log.Printf("ZMQ proxy subscribed to topic: %s", topic)

	// Use ZMQ's built-in proxy - blocks until context is terminated
	// This is implemented in C and is highly optimized for XSUB/XPUB forwarding
	err := zmq.Proxy(z.frontend, z.backend, nil)
	if err != nil {
		log.Printf("ZMQ proxy stopped: %v", err)
	}
}

func (z *ZMQProxy) Stop() {
	z.mu.Lock()
	if !z.running {
		z.mu.Unlock()
		return
	}
	z.running = false
	z.mu.Unlock()

	// Terminating context will cause zmq.Proxy() to return with an error
	if z.ctx != nil {
		z.ctx.Term()
	}
	<-z.stoppedCh
}

func (z *ZMQProxy) cleanup() {
	if z.frontend != nil {
		z.frontend.Close()
	}
	if z.backend != nil {
		z.backend.Close()
	}
	// Note: context is terminated in Stop() to interrupt zmq.Proxy()
}
