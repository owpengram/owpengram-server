package loadharness

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iamxvbaba/td/exchange"
	"github.com/iamxvbaba/td/mtproto"
	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/telegram"
	"github.com/iamxvbaba/td/telegram/dcs"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/transport"
)

type clientHooks struct {
	Update          telegram.UpdateHandler
	ConnectionState func(telegram.ConnectionState)
	Dead            func(error)
	Device          *telegram.DeviceConfig
}

// loadMessageIDSource keeps the load generator on the same MTProto message-id
// rules as a production client even when the host clock lands exactly on an
// integral second. The underlying gotd generator can emit a client id whose
// lower 32 bits are zero in that narrow window; Telegram explicitly forbids
// that value as replay protection. Retrying also fences any encoded duplicate
// caused by a very low-resolution clock without weakening the DUT validator.
type loadMessageIDSource struct {
	mu     sync.Mutex
	source mtproto.MessageIDSource
	last   int64
}

func newLoadMessageIDSource(now func() time.Time) *loadMessageIDSource {
	return &loadMessageIDSource{source: proto.NewMessageIDGen(now)}
}

func (s *loadMessageIDSource) New(messageType proto.MessageType) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		messageID := s.source.New(messageType)
		if messageID <= s.last {
			continue
		}
		if messageType == proto.MessageFromClient && uint32(messageID) == 0 {
			continue
		}
		s.last = messageID
		return messageID
	}
}

func newClient(endpoint Endpoint, publicKey *rsa.PublicKey, storage telegram.SessionStorage, hooks clientHooks) (*telegram.Client, error) {
	host, portText, err := net.SplitHostPort(endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid endpoint port %q", portText)
	}
	protocol := dcs.Protocol(transport.Intermediate)
	if endpoint.Obfuscated {
		protocol = transport.Abridged
	}
	resolver := dcs.Plain(dcs.PlainOptions{Protocol: protocol, Obfuscated: endpoint.Obfuscated})
	updateHandler := hooks.Update
	if updateHandler == nil {
		updateHandler = telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil })
	}
	device := telegram.DeviceTDesktopWindows()
	if hooks.Device != nil {
		device = *hooks.Device
	}
	return telegram.NewClient(endpoint.APIID, endpoint.APIHash, telegram.Options{
		PublicKeys: []exchange.PublicKey{{RSA: publicKey}},
		DC:         endpoint.DC,
		Resolver:   resolver,
		DCList: dcs.List{Options: []tg.DCOption{{
			ID: endpoint.DC, IPAddress: host, Port: port, Static: true,
		}}},
		SessionStorage:    storage,
		UpdateHandler:     updateHandler,
		EnablePFS:         endpoint.PFS,
		TempKeyTTL:        endpoint.TempKeyTTL,
		Device:            device,
		MessageID:         newLoadMessageIDSource(time.Now),
		OnConnectionState: hooks.ConnectionState,
		OnDead:            hooks.Dead,
	}), nil
}

func loadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read RSA key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("RSA key is not PEM")
	}
	if private, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return &private.PublicKey, nil
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if private, ok := parsed.(*rsa.PrivateKey); ok {
			return &private.PublicKey, nil
		}
	}
	if public, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return public, nil
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if public, ok := parsed.(*rsa.PublicKey); ok {
			return public, nil
		}
	}
	return nil, errors.New("PEM does not contain an RSA private or public key")
}

func writePortablePublicKey(manifestPath, sourcePath string) (string, *rsa.PublicKey, error) {
	publicKey, err := loadRSAPublicKey(sourcePath)
	if err != nil {
		return "", nil, err
	}
	encoded, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", nil, err
	}
	const name = "server_rsa_public.pem"
	path := filepath.Join(filepath.Dir(manifestPath), name)
	data := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return "", nil, err
	}
	return name, publicKey, nil
}

func loadManifestPublicKey(manifestPath string, endpoint Endpoint, override string) (*rsa.PublicKey, error) {
	path := strings.TrimSpace(override)
	if path == "" {
		path = endpoint.RSAKeyPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(manifestPath), filepath.FromSlash(path))
		}
	}
	return loadRSAPublicKey(path)
}
