package main

// Google tests Android apps automatically (Google Play Protect, right after a new version): the test types random
// usernames into the login screen, so they reach the server. The log says so, instead of looking like an attack, and
// the attempts are not counted as refused logins. Only Google's own networks – not Google Cloud, where anybody can
// rent a machine.

import (
	"net/netip"
	"sync"
)

var googleNets = sync.OnceValue(func() []netip.Prefix {
	return parseNets([]string{"64.233.160.0/19", "66.102.0.0/20", "66.249.64.0/19", "72.14.192.0/18", "74.125.0.0/16",
		"108.177.0.0/17", "142.250.0.0/15", "172.217.0.0/16", "173.194.0.0/16", "209.85.128.0/17", "216.58.192.0/19",
		"216.239.32.0/19"})
})

// googleTest: the address belongs to Google itself.
func googleTest(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, p := range googleNets() {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}
