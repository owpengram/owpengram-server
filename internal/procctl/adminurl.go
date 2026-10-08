package procctl

import "strings"

// AdminUIURL rewrites TELESRV_ADMIN_UI_ADDR (read from groups, as returned
// by ReadEnvGroups) into something a browser can actually open, same as
// server-panel.py's browsable_host_port(): a wildcard bind (0.0.0.0, ::,
// empty) displays as loopback, since the bind itself is never something to
// type into a browser. ok is false when the field is empty (unset).
//
// Shared by cmd/owpengram-ctl's plain-text output and internal/panel's TUI,
// so the two never drift apart on how they display the same address.
func AdminUIURL(groups []EnvGroup) (url string, ok bool) {
	addr := EnvGroupValue(groups, "TELESRV_ADMIN_UI_ADDR")
	if addr == "" {
		return "", false
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		scheme, rest, _ := strings.Cut(addr, "://")
		netloc, slash, path := cutFirst(rest, "/")
		return scheme + "://" + BrowsableHostPort(netloc) + slash + path, true
	}
	return "http://" + BrowsableHostPort(addr), true
}

func cutFirst(s, sep string) (before, sepFound, after string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], sep, s[i+len(sep):]
	}
	return s, "", ""
}

// BrowsableHostPort rewrites a wildcard bind address (0.0.0.0, ::, empty,
// *) into a loopback address a browser can actually connect to. Any other
// host:port is returned unchanged.
func BrowsableHostPort(hostPort string) string {
	s := strings.TrimSpace(hostPort)
	if strings.HasPrefix(s, "[") {
		closeIdx := strings.Index(s, "]")
		if closeIdx == -1 {
			return s
		}
		host, rest := s[1:closeIdx], s[closeIdx+1:]
		if host == "::" || host == "" {
			return "[::1]" + rest
		}
		return "[" + host + "]" + rest
	}
	lastColon := strings.LastIndex(s, ":")
	if lastColon < 0 {
		return s
	}
	host, port := s[:lastColon], s[lastColon+1:]
	switch host {
	case "0.0.0.0", "", "*":
		return "127.0.0.1:" + port
	case "::":
		return "[::1]:" + port
	default:
		return s
	}
}

// EnvGroupValue looks up a single field's current effective value across
// every group ReadEnvGroups returned.
func EnvGroupValue(groups []EnvGroup, key string) string {
	for _, g := range groups {
		for _, f := range g.Fields {
			if f.Key == key {
				return f.Value
			}
		}
	}
	return ""
}
