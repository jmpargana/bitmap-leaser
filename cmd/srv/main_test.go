package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jmpargana/bitmap-leaser/internal/allocator"
)

func setupServer(t *testing.T) *Server {
	a, err := allocator.New("10.0.0.0/30")
	if err != nil {
		t.Fatalf("failed to create allocator: %v", err)
	}
	t.Cleanup(func() { a.Stop() })
	return New(a)
}

func TestHealthHandler(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.Health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Health() status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response error: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("status = %q, want 'ok'", resp["status"])
	}
}

func TestAllocateSuccess(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/ip", nil)
	w := httptest.NewRecorder()

	srv.Allocate(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Allocate() status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp LeaseResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response error: %v", err)
	}

	if resp.IP == "" {
		t.Error("IP is empty")
	}

	if resp.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero")
	}

	if resp.LastSeen.IsZero() {
		t.Error("LastSeen is zero")
	}
}

func TestAllocateMultiple(t *testing.T) {
	srv := setupServer(t)

	ips := make(map[string]bool)
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("GET", "/ip", nil)
		w := httptest.NewRecorder()

		srv.Allocate(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("iter %d: status = %d, want %d", i, w.Code, http.StatusOK)
			continue
		}

		var resp LeaseResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("iter %d: decode error: %v", i, err)
		}

		if ips[resp.IP] {
			t.Errorf("iter %d: duplicate IP allocated: %s", i, resp.IP)
		}
		ips[resp.IP] = true
	}

	if len(ips) != 4 {
		t.Errorf("allocated %d unique IPs, want 4", len(ips))
	}
}

func TestAllocateFull(t *testing.T) {
	srv := setupServer(t)

	// Allocate all 4 IPs
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("GET", "/ip", nil)
		w := httptest.NewRecorder()
		srv.Allocate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("iter %d: unexpected status %d", i, w.Code)
		}
	}

	// 5th should fail
	req := httptest.NewRequest("GET", "/ip", nil)
	w := httptest.NewRecorder()
	srv.Allocate(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Allocate() full status = %d, want %d", w.Code, http.StatusConflict)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response error: %v", err)
	}

	if resp.Error == "" {
		t.Error("Error message empty")
	}
}

func TestRenewSuccess(t *testing.T) {
	srv := setupServer(t)

	// First allocate
	req := httptest.NewRequest("GET", "/ip", nil)
	w := httptest.NewRecorder()
	srv.Allocate(w, req)

	var lease LeaseResponse
	json.NewDecoder(w.Body).Decode(&lease)

	// Then renew
	req = httptest.NewRequest("PUT", fmt.Sprintf("/ip/%s/renew", lease.IP), nil)
	req.SetPathValue("ip", lease.IP)
	w = httptest.NewRecorder()

	srv.Renew(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Renew() status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["status"] != "renewed" {
		t.Errorf("status = %q, want 'renewed'", resp["status"])
	}
}

func TestRenewInvalidIP(t *testing.T) {
	srv := setupServer(t)

	tests := []struct {
		name string
		ip   string
	}{
		{"invalid", "not-an-ip"},
		{"ipv6", "2001:db8::1"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("PUT", "/ip/{ip}/renew", nil)
			req.SetPathValue("ip", tt.ip)
			w := httptest.NewRecorder()

			srv.Renew(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp ErrorResponse
			json.NewDecoder(w.Body).Decode(&resp)

			if resp.Error == "" {
				t.Error("Error message empty")
			}
		})
	}
}

func TestRenewNotLeased(t *testing.T) {
	srv := setupServer(t)

	ip := "10.0.0.100"
	req := httptest.NewRequest("PUT", "/ip/{ip}/renew", nil)
	req.SetPathValue("ip", ip)
	w := httptest.NewRecorder()

	srv.Renew(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Renew() not-leased status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp ErrorResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error == "" {
		t.Error("Error message empty")
	}
}

func TestReleaseSuccess(t *testing.T) {
	srv := setupServer(t)

	// First allocate
	req := httptest.NewRequest("GET", "/ip", nil)
	w := httptest.NewRecorder()
	srv.Allocate(w, req)

	var lease LeaseResponse
	json.NewDecoder(w.Body).Decode(&lease)

	// Then release
	req = httptest.NewRequest("DELETE", "/ip/{ip}", nil)
	req.SetPathValue("ip", lease.IP)
	w = httptest.NewRecorder()

	srv.Release(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Release() status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Body should be empty for 204
	body, _ := io.ReadAll(w.Body)
	if len(body) > 0 {
		t.Errorf("Release() body should be empty, got %d bytes", len(body))
	}
}

func TestReleaseInvalidIP(t *testing.T) {
	srv := setupServer(t)

	tests := []struct {
		name string
		ip   string
	}{
		{"invalid", "not-an-ip"},
		{"ipv6", "::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("DELETE", "/ip/{ip}", nil)
			req.SetPathValue("ip", tt.ip)
			w := httptest.NewRecorder()

			srv.Release(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp ErrorResponse
			json.NewDecoder(w.Body).Decode(&resp)

			if resp.Error == "" {
				t.Error("Error message empty")
			}
		})
	}
}

func TestReleaseNotLeased(t *testing.T) {
	srv := setupServer(t)

	ip := "10.0.0.100"
	req := httptest.NewRequest("DELETE", "/ip/{ip}", nil)
	req.SetPathValue("ip", ip)
	w := httptest.NewRecorder()

	srv.Release(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Release() not-leased status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp ErrorResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error == "" {
		t.Error("Error message empty")
	}
}

func TestAllocateReleaseRealloc(t *testing.T) {
	srv := setupServer(t)

	// Allocate first IP
	req := httptest.NewRequest("GET", "/ip", nil)
	w := httptest.NewRecorder()
	srv.Allocate(w, req)

	var lease1 LeaseResponse
	json.NewDecoder(w.Body).Decode(&lease1)
	firstIP := lease1.IP

	// Release it
	req = httptest.NewRequest("DELETE", "/ip/{ip}", nil)
	req.SetPathValue("ip", firstIP)
	w = httptest.NewRecorder()
	srv.Release(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("Release() status = %d", w.Code)
	}

	// Allocate again - should get same IP
	req = httptest.NewRequest("GET", "/ip", nil)
	w = httptest.NewRecorder()
	srv.Allocate(w, req)

	var lease2 LeaseResponse
	json.NewDecoder(w.Body).Decode(&lease2)

	if lease2.IP != firstIP {
		t.Errorf("reallocated IP %s, want %s", lease2.IP, firstIP)
	}
}

func TestConcurrentAllocate(t *testing.T) {
	srv := setupServer(t)

	numGoroutines := 10
	numReqs := 3
	results := make(chan string, numGoroutines*numReqs)
	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < numReqs; i++ {
				req := httptest.NewRequest("GET", "/ip", nil)
				w := httptest.NewRecorder()
				srv.Allocate(w, req)

				if w.Code == http.StatusOK {
					var resp LeaseResponse
					json.NewDecoder(w.Body).Decode(&resp)
					results <- resp.IP
				}
			}
		}()
	}

	wg.Wait()
	close(results)

	// Check no duplicates
	ips := make(map[string]bool)
	for ip := range results {
		if ips[ip] {
			t.Errorf("duplicate IP allocated: %s", ip)
		}
		ips[ip] = true
	}

	// Should have allocated 4 unique IPs (pool size), rest failed
	if len(ips) > 4 {
		t.Errorf("allocated %d IPs, pool size 4", len(ips))
	}
}

func TestConcurrentMixed(t *testing.T) {
	a, err := allocator.New("10.0.0.0/25")
	if err != nil {
		t.Fatalf("failed to create allocator: %v", err)
	}
	defer a.Stop()

	srv := New(a)
	numGoroutines := 10
	var wg sync.WaitGroup
	var successCount int64

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Allocate
			req := httptest.NewRequest("GET", "/ip", nil)
			w := httptest.NewRecorder()
			srv.Allocate(w, req)

			if w.Code != http.StatusOK {
				return
			}

			var lease LeaseResponse
			json.NewDecoder(w.Body).Decode(&lease)

			// Renew
			req = httptest.NewRequest("PUT", "/ip/{ip}/renew", nil)
			req.SetPathValue("ip", lease.IP)
			w = httptest.NewRecorder()
			srv.Renew(w, req)

			if w.Code != http.StatusOK {
				return
			}

			// Release
			req = httptest.NewRequest("DELETE", "/ip/{ip}", nil)
			req.SetPathValue("ip", lease.IP)
			w = httptest.NewRecorder()
			srv.Release(w, req)

			if w.Code == http.StatusNoContent {
				successCount++
			}
		}()
	}

	wg.Wait()

	if successCount == 0 {
		t.Error("no successful operations under concurrency")
	}
}

func TestResponseHeaders(t *testing.T) {
	srv := setupServer(t)

	tests := []struct {
		name     string
		handler  func(*Server, http.ResponseWriter, *http.Request)
		req      *http.Request
		wantCode int
	}{
		{
			name:     "Health",
			handler:  (*Server).Health,
			req:      httptest.NewRequest("GET", "/health", nil),
			wantCode: http.StatusOK,
		},
		{
			name:     "Allocate",
			handler:  (*Server).Allocate,
			req:      httptest.NewRequest("GET", "/ip", nil),
			wantCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.handler(srv, w, tt.req)

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

func BenchmarkAllocate(b *testing.B) {
	a, err := allocator.New("10.0.0.0/24")
	if err != nil {
		b.Fatalf("failed to create allocator: %v", err)
	}
	defer a.Stop()

	srv := New(a)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/ip", nil)
		w := httptest.NewRecorder()
		srv.Allocate(w, req)

		// Don't care about the result for benchmark
	}
}

func BenchmarkRenew(b *testing.B) {
	a, err := allocator.New("10.0.0.0/24")
	if err != nil {
		b.Fatalf("failed to create allocator: %v", err)
	}
	defer a.Stop()

	srv := New(a)

	// Pre-allocate one IP
	req := httptest.NewRequest("GET", "/ip", nil)
	w := httptest.NewRecorder()
	srv.Allocate(w, req)

	var lease LeaseResponse
	json.NewDecoder(w.Body).Decode(&lease)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("PUT", "/ip/{ip}/renew", nil)
		req.SetPathValue("ip", lease.IP)
		w := httptest.NewRecorder()
		srv.Renew(w, req)
	}
}
