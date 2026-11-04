package loadharness

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/owpengram/owpengram-td/session"
)

const encryptedSessionMagic = "TLSLOAD1"

// EncryptedFileStorage encrypts gotd's complete session blob with AES-256-GCM.
// A unique random nonce is generated on every replacement and the file is
// written with owner-only permissions.
type EncryptedFileStorage struct {
	Path string
	Key  [32]byte
	mu   sync.Mutex
}

func (s *EncryptedFileStorage) LoadSession(context.Context) ([]byte, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil, errors.New("invalid encrypted session storage")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read encrypted session: %w", err)
	}
	block, err := aes.NewCipher(s.Key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	header := len(encryptedSessionMagic) + gcm.NonceSize()
	if len(data) < header || string(data[:len(encryptedSessionMagic)]) != encryptedSessionMagic {
		return nil, errors.New("encrypted session has an invalid header")
	}
	nonce := data[len(encryptedSessionMagic):header]
	plain, err := gcm.Open(nil, nonce, data[header:], []byte(encryptedSessionMagic))
	if err != nil {
		return nil, errors.New("encrypted session authentication failed")
	}
	return plain, nil
}

func (s *EncryptedFileStorage) StoreSession(_ context.Context, plain []byte) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return errors.New("invalid encrypted session storage")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	block, err := aes.NewCipher(s.Key[:])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate session nonce: %w", err)
	}
	data := make([]byte, 0, len(encryptedSessionMagic)+len(nonce)+len(plain)+gcm.Overhead())
	data = append(data, encryptedSessionMagic...)
	data = append(data, nonce...)
	data = gcm.Seal(data, nonce, plain, []byte(encryptedSessionMagic))
	return writeFileAtomic(s.Path, data, 0o600)
}

func GenerateSessionKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing session key %q", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(key[:]) + "\n"
	return writeFileAtomic(path, []byte(encoded), 0o600)
}

func LoadSessionKey(path string) ([32]byte, error) {
	var key [32]byte
	info, err := os.Stat(path)
	if err != nil {
		return key, fmt.Errorf("stat session key: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return key, fmt.Errorf("session key %q must not be group/world accessible (mode %o)", path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return key, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != len(key) {
		return key, errors.New("session key must be base64-encoded 32 bytes")
	}
	copy(key[:], decoded)
	return key, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".telesrv-load-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// On Unix rename atomically replaces. Windows requires removing the old
	// destination first; session files remain recoverable from the complete temp
	// file if that narrow replacement fails.
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}
