package main

// A reverse proxy in front of a standalone ArgusSen (nginx, Caddy, Traefik, a tunnel …): only proxies listed in
// "trusted_proxies" are believed about the client's address (X-Forwarded-For) – anybody can send that header.

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

var trustedProxies []netip.Prefix

// parseNets: "10.0.0.5" or "10.20.0.0/16" → networks (invalid entries are skipped).
func parseNets(list []string) []netip.Prefix {
	var out []netip.Prefix
	for _, v := range list {
		v = strings.TrimSpace(v)
		if p, err := netip.ParsePrefix(v); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(v); err == nil {
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	return out
}

// proxiedIP: the client's address reported by a trusted proxy (the last address in X-Forwarded-For), or nil.
func proxiedIP(r *http.Request) net.IP {
	if len(trustedProxies) == 0 {
		return nil
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		return nil
	}
	trusted := false
	for _, p := range trustedProxies {
		if p.Contains(a.Unmap()) {
			trusted = true
		}
	}
	xff := r.Header.Get("X-Forwarded-For")
	if !trusted || xff == "" {
		return nil
	}
	parts := strings.Split(xff, ",")
	return net.ParseIP(strings.TrimSpace(parts[len(parts)-1]))
}

// trustedLAN: the request came through a trusted proxy from a client on a private network.
func trustedLAN(r *http.Request) bool {
	ip := proxiedIP(r)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}
