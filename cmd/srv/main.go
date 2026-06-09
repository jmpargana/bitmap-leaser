package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmpargana/bitmap-leaser/internal/allocator"
)

const (
	prefix   = "10.0.0.0/24"
	addr     = ":8080"
	shutdown = 30 * time.Second
)

type Server struct {
	alloc *allocator.Allocator
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type LeaseResponse struct {
	IP        string    `json:"ip"`
	ExpiresAt time.Time `json:"expires_at"`
	LastSeen  time.Time `json:"last_seen"`
}

func New(alloc *allocator.Allocator) *Server {
	return &Server{alloc: alloc}
}

func (s *Server) Allocate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	lease, err := s.alloc.Allocate()
	if err != nil {
		w.WriteHeader(http.StatusConflict) // 409: no IPs available
		json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(LeaseResponse{
		IP:        allocator.Uint32ToIp(lease.IP).String(),
		ExpiresAt: lease.ExpiresAt,
		LastSeen:  lease.LastSeen,
	})
}

func (s *Server) Renew(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rawIP := r.PathValue("ip")
	ip := net.ParseIP(rawIP)
	if ip == nil || ip.To4() == nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid IPv4"})
		return
	}

	if err := s.alloc.Renew(ip); err != nil {
		w.WriteHeader(http.StatusNotFound) // 404: IP not leased
		json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "renewed"})
}

func (s *Server) Release(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rawIP := r.PathValue("ip")
	ip := net.ParseIP(rawIP)
	if ip == nil || ip.To4() == nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid IPv4"})
		return
	}

	if err := s.alloc.Release(ip); err != nil {
		w.WriteHeader(http.StatusNotFound) // 404: IP not leased
		json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func main() {
	alloc, err := allocator.New(prefix)
	if err != nil {
		log.Fatalf("failed to create allocator: %v", err)
	}
	defer alloc.Stop()

	srv := New(alloc)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", srv.Health)
	mux.HandleFunc("GET /ip", srv.Allocate)
	mux.HandleFunc("PUT /ip/{ip}/renew", srv.Renew)
	mux.HandleFunc("DELETE /ip/{ip}", srv.Release)

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errChan := make(chan error, 1)

	go func() {
		log.Printf("Starting server on %s", addr)
		errChan <- httpSrv.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.Printf("Received signal: %v", sig)
	case err := <-errChan:
		if err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdown)
	defer cancel()

	log.Println("Shutting down server gracefully...")
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
		os.Exit(1)
	}

	log.Println("Server stopped")
}
