//go:build !private

package main

// The public build (the Home Assistant app etc.): the private functions (private.go) do not exist here.

import (
	"net"
	"net/http"
)

func loadPrivate(string)                                      {}
func (s *Server) privateRoutes(mux, api *http.ServeMux)       {}
func withPrivateHAComponents(c map[string]any) map[string]any { return c }
func (h *haBridge) sendAccess()                               {}
func (s *camStore) writeList()                                {}

func startProfiling() {}

// origin: where a request came from (access log). Through Home Assistant (ingress) = "ha", otherwise "local".
func (s *Server) origin(r *http.Request) string {
	if s.isIngress(r) {
		return "ha"
	}
	return "local"
}

// clientIP: the address of the sender (behind a trusted reverse proxy: the address it reports).
func clientIP(r *http.Request) string {
	if ip := proxiedIP(r); ip != nil {
		return ip.String()
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}
