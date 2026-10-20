package server

import (
	"net"
	"net/http"
)

// loopbackHost reports whether a Host header names this machine's loopback.
func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

// LoopbackOnly refuses every request whose Host is not loopback. The dashboard
// binds to 127.0.0.1, but a web page can still reach it by rebinding its own
// DNS name to 127.0.0.1; its requests then carry that name as Host and would
// otherwise read the Process command lines. Reads need this as much as writes.
func LoopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "this dashboard answers only on 127.0.0.1 or localhost", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
