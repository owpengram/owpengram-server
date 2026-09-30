package procctl

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
)

// ServerAddress returns the MTProto address clients connect to (advertise
// IP + listen port), read straight from .env -- present as soon as it's
// configured, even before the first Start. ok is false when either
// TELESRV_ADVERTISE_IP or TELESRV_LISTEN is unset, or the listen address's
// port isn't parseable.
func (m *Manager) ServerAddress() (address string, ok bool) {
	values, err := m.readEnvFile()
	if err != nil {
		return "", false
	}
	ip := values["TELESRV_ADVERTISE_IP"]
	listen := values["TELESRV_LISTEN"]
	if ip == "" || listen == "" {
		return "", false
	}
	idx := strings.LastIndex(listen, ":")
	if idx < 0 || idx == len(listen)-1 {
		return "", false
	}
	port := listen[idx+1:]
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return ip + ":" + port, true
}

// ServerPublicKeyPEM derives the server's RSA public key (PKCS#1 "RSA
// PUBLIC KEY" PEM, the format patched into client builds) from the private
// key file (TELESRV_RSA_KEY, default data/server_rsa.pem -- see
// internal/mtprotoedge/rsakey.go for the matching writer). That file is
// only generated the first time owpengram-server actually starts, so ok is
// legitimately false on a never-started install -- and on any missing,
// corrupt, or unparseable file, rather than erroring.
func (m *Manager) ServerPublicKeyPEM() (pemText string, ok bool) {
	values, _ := m.readEnvFile()
	keyPath := values["TELESRV_RSA_KEY"]
	if keyPath == "" {
		keyPath = "data/server_rsa.pem"
	}
	if !filepath.IsAbs(keyPath) {
		keyPath = filepath.Join(m.Root, keyPath)
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", false
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", false
	}
	der := x509.MarshalPKCS1PublicKey(&priv.PublicKey)
	out := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: der})
	return strings.TrimSpace(string(out)), true
}
