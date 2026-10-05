package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRESTServerUsesIPv4Loopback(t *testing.T) {
	if got := newRESTServer(3456).Addr; got != "127.0.0.1:3456" {
		t.Fatalf("REST server must be local-only, got %q", got)
	}
	server := newRESTServer(0)
	if server.Handler != nil {
		t.Fatal("existing default mux must be preserved")
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close(); listener.Close() })
	address := listener.Addr().(*net.TCPAddr)
	if !address.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("non-loopback listener: %s", address)
	}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "local fixture") })
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "local fixture" {
		t.Fatalf("unexpected fixture response: %q %v", body, err)
	}
	server.Close()
	if err := <-done; err != http.ErrServerClosed {
		t.Fatalf("server shutdown: %v", err)
	}
}
