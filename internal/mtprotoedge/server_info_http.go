package mtprotoedge

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
)

// ServerInfoPath is the well-known same-port HTTP path a client can GET to
// self-configure an "Add Server" entry from just a host:port -- no more
// manual `openssl rsa -RSAPublicKey_out` + copy-paste of the PEM block.
//
// Served over the same samePortMux HTTP side that already carries WebSocket
// upgrades (see same_port_mux.go): a plain GET here never looks like an
// obfuscated2 init (isHTTPHeaderPrefix's "legal init headers never start
// with GET/POST/HEAD/OPTI" invariant), so this adds no new port and no new
// risk to the raw-TCP detection path. Only reachable when serveMixed is
// active, i.e. TELESRV_WEBSOCKET_ENABLE=true (the default).
const ServerInfoPath = "/owpengram/server-info"

// ServerInfoResponse is ServerInfoPath's JSON body. RSAPublicKeyPEM is the
// PKCS#1 "RSA PUBLIC KEY" PEM block -- the same format
// `openssl rsa -RSAPublicKey_out` produces, and what the client's "Add
// Server" RSA key field already expects verbatim.
type ServerInfoResponse struct {
	DCID            int    `json:"dc_id"`
	RSAPublicKeyPEM string `json:"rsa_public_key_pem"`
}

// rsaPublicKeyPEM renders key's public half as a PKCS#1 PEM block, matching
// `openssl rsa -in server_rsa.pem -RSAPublicKey_out` byte-for-byte (that
// flag selects PKCS#1 encoding, not the x509/PKIX default `-pubout` would
// produce -- a real format difference, not just a header/footer string).
func rsaPublicKeyPEM(key *rsa.PrivateKey) []byte {
	if key == nil {
		return nil
	}
	der := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: der})
}

// serverInfoHTTPHandler serves ServerInfoResponse at ServerInfoPath and
// delegates every other path to next (the existing WebSocket route
// handler). pubKeyPEM is nil when the server has no RSA key configured
// (shouldn't happen in production -- handshakes would already be broken --
// but a client asking anyway gets 503, not a panic or an empty key).
func serverInfoHTTPHandler(next http.Handler, dc int, pubKeyPEM []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ServerInfoPath {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if len(pubKeyPEM) == 0 {
			http.Error(w, "server key not configured", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(ServerInfoResponse{
			DCID:            dc,
			RSAPublicKeyPEM: string(pubKeyPEM),
		})
	})
}
