//go:build e2e

// Package fakellm serves the genuine Anthropic and OpenAI-compatible wire
// protocols from a local HTTP server, so aka's real provider code performs real
// requests with real JSON parsing against responses the test controls.
//
// It records every request, which is what lets tests assert on the bytes that
// actually crossed the socket rather than on a function's return value.
package fakellm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Recorded is one request the server received.
type Recorded struct {
	Path   string
	Header http.Header
	Body   []byte
}

// Responder produces the status and body for a request.
type Responder func(req Recorded) (status int, body []byte)

// Server is a recording, programmable stand-in for an LLM provider endpoint.
type Server struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	responder Responder
	requests  []Recorded
}

// New starts a server on 127.0.0.1. The loopback bind satisfies aka's
// AKA_LLM_BASE_URL guard without any special handling. It is shut down at test
// cleanup.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{t: t, responder: Normal()}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the server's base URL, suitable for AKA_LLM_BASE_URL.
func (s *Server) URL() string { return s.srv.URL }

// Respond sets the responder used for subsequent requests.
func (s *Server) Respond(r Responder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responder = r
}

// Requests returns a copy of everything received so far.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Recorded, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}

	rec := Recorded{Path: r.URL.Path, Header: r.Header.Clone(), Body: body}

	s.mu.Lock()
	s.requests = append(s.requests, rec)
	responder := s.responder
	s.mu.Unlock()

	status, respBody := responder(rec)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(respBody); err != nil {
		s.t.Errorf("write response: %v", err)
	}
}
