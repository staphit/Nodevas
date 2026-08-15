package server

import (
	"net"
	"strings"
)

// allowsHost reports whether the request may be served under the Host it
// claims. The port is ignored: it is the name that a rebinding attack has to
// bring along, and a proxy may forward the request on a different one. A nil
// map is the unconfigured zero value only; UseListenAddress never produces one.
func (s *Server) allowsHost(host string) bool {
	if s.allowedHosts == nil {
		return true
	}
	return s.allowedHosts[hostNameOnly(host)]
}

func addLoopbackNames(allowed map[string]bool) {
	allowed["localhost"] = true
	allowed["127.0.0.1"] = true
	allowed["::1"] = true
}

func isLoopbackName(name string) bool {
	if name == "localhost" {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// hostNameOnly drops the port from a Host header value. A bare IPv6 literal
// has no port to split off, hence the fall back to the value as given.
func hostNameOnly(value string) string {
	if name, _, err := net.SplitHostPort(value); err == nil {
		value = name
	}
	return normalizeHostName(value)
}

func normalizeHostName(value string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), "[]"))
}
