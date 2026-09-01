package loadharness

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iamxvbaba/td/session"
	"github.com/iamxvbaba/td/tg"
)

type ProvisionConfig struct {
	ManifestPath    string
	SessionKeyPath  string
	RSAKeyPath      string
	Endpoint        Endpoint
	Accounts        int
	ExtraDevices    int
	Concurrency     int
	PhonePrefix     string
	Code            string
	FirstNamePrefix string
}

// DefaultPhonePrefix plus the six-digit account index produces the repository's
// structurally possible reserved NANP range (for example +1 555 000 0001).
const DefaultPhonePrefix = "+15550"

type ProvisionEvent struct {
	Completed int
	Total     int
	Session   SessionRecord
	Resumed   bool
	Err       error
}

func (c ProvisionConfig) validate() error {
	if err := c.Endpoint.Validate(); err != nil {
		return err
	}
	if c.ManifestPath == "" || c.SessionKeyPath == "" || c.RSAKeyPath == "" {
		return errors.New("manifest, session-key and RSA key paths are required")
	}
	if c.Accounts <= 0 || c.ExtraDevices < 0 || c.ExtraDevices > c.Accounts {
		return errors.New("accounts must be positive and extra-devices must be between zero and accounts")
	}
	if c.Concurrency <= 0 || c.Concurrency > 64 {
		return errors.New("provision concurrency must be between 1 and 64")
	}
	if strings.TrimSpace(c.Code) == "" {
		return errors.New("a test login code is required")
	}
	return nil
}

// Provision creates accounts only through auth.sendCode/signIn/signUp. Primary
// devices finish before duplicate-device login starts, preventing two workers
// from racing the first signup for one phone.
func Provision(ctx context.Context, cfg ProvisionConfig, progress func(ProvisionEvent)) (*Manifest, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	key, err := LoadSessionKey(cfg.SessionKeyPath)
	if err != nil {
		return nil, err
	}
	publicName, publicKey, err := writePortablePublicKey(cfg.ManifestPath, cfg.RSAKeyPath)
	if err != nil {
		return nil, err
	}
	cfg.Endpoint.RSAKeyPath = publicName

	primary := make([]SessionRecord, 0, cfg.Accounts)
	for account := 0; account < cfg.Accounts; account++ {
		primary = append(primary, desiredSessionRecord(account, account, 0, cfg))
	}
	completed, err := provisionPhase(ctx, cfg, key, publicKey, primary, progress, 0, cfg.Accounts+cfg.ExtraDevices)
	if err != nil {
		return nil, err
	}
	extra := make([]SessionRecord, 0, cfg.ExtraDevices)
	for account := 0; account < cfg.ExtraDevices; account++ {
		extra = append(extra, desiredSessionRecord(cfg.Accounts+account, account, 1, cfg))
	}
	extraCompleted, err := provisionPhase(ctx, cfg, key, publicKey, extra, progress, len(completed), cfg.Accounts+cfg.ExtraDevices)
	if err != nil {
		return nil, err
	}
	completed = append(completed, extraCompleted...)
	sort.Slice(completed, func(i, j int) bool { return completed[i].Index < completed[j].Index })
	manifest := &Manifest{
		Version: ManifestVersion, CreatedAt: time.Now().UTC(), Endpoint: cfg.Endpoint, Sessions: completed,
	}
	if err := WriteManifest(cfg.ManifestPath, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func desiredSessionRecord(index, account, device int, cfg ProvisionConfig) SessionRecord {
	return SessionRecord{
		Index: index, AccountIndex: account, DeviceIndex: device,
		Phone:       fmt.Sprintf("%s%06d", cfg.PhonePrefix, account+1),
		FirstName:   fmt.Sprintf("%s%04d", cfg.FirstNamePrefix, account+1),
		SessionFile: filepath.ToSlash(filepath.Join(sessionDirectoryForManifest(cfg.ManifestPath), fmt.Sprintf("session-%04d-device-%d.bin", account, device))),
	}
}

// sessionDirectoryForManifest keeps independently named manifests in the same
// parent directory from ever sharing encrypted session files. The conventional
// manifest.json path retains the compact "sessions" directory, so moving a
// complete bundle to another host remains portable.
func sessionDirectoryForManifest(manifestPath string) string {
	base := filepath.Base(filepath.Clean(manifestPath))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "." || strings.EqualFold(base, "manifest") {
		return "sessions"
	}
	return "sessions-" + base
}

func provisionPhase(
	ctx context.Context,
	cfg ProvisionConfig,
	key [32]byte,
	publicKey *rsa.PublicKey,
	desired []SessionRecord,
	progress func(ProvisionEvent),
	completedBefore, total int,
) ([]SessionRecord, error) {
	if len(desired) == 0 {
		return nil, nil
	}
	type result struct {
		record  SessionRecord
		resumed bool
		err     error
	}
	jobs := make(chan SessionRecord)
	results := make(chan result, len(desired))
	workers := min(cfg.Concurrency, len(desired))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for record := range jobs {
				path := resolveSessionPath(cfg.ManifestPath, record)
				_, statErr := os.Stat(path)
				resumed := statErr == nil
				storage := &EncryptedFileStorage{Path: path, Key: key}
				user, err := provisionOne(ctx, cfg, publicKey, storage, record)
				if err == nil {
					record.UserID = user.ID
					record.AccessHash = user.AccessHash
				}
				results <- result{record: record, resumed: resumed, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, record := range desired {
			select {
			case jobs <- record:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	completed := make([]SessionRecord, 0, len(desired))
	var firstErr error
	for result := range results {
		if result.err == nil {
			completed = append(completed, result.record)
		} else if firstErr == nil {
			firstErr = fmt.Errorf("provision session %d: %w", result.record.Index, result.err)
		}
		if progress != nil {
			progress(ProvisionEvent{
				Completed: completedBefore + len(completed), Total: total,
				Session: result.record, Resumed: result.resumed, Err: result.err,
			})
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if len(completed) != len(desired) {
		return nil, ctx.Err()
	}
	return completed, nil
}

func provisionOne(ctx context.Context, cfg ProvisionConfig, publicKey *rsa.PublicKey, storage *EncryptedFileStorage, record SessionRecord) (*tg.User, error) {
	client, err := newClient(cfg.Endpoint, publicKey, storage, clientHooks{})
	if err != nil {
		return nil, err
	}
	var user *tg.User
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("authorization status: %w", err)
		}
		if status.Authorized && status.User != nil {
			user = status.User
			return nil
		}
		raw := tg.NewClient(client)
		sent, err := raw.AuthSendCode(ctx, &tg.AuthSendCodeRequest{
			PhoneNumber: record.Phone, APIID: cfg.Endpoint.APIID, APIHash: cfg.Endpoint.APIHash, Settings: tg.CodeSettings{},
		})
		if err != nil {
			return fmt.Errorf("auth.sendCode: %w", err)
		}
		sentCode, ok := sent.(*tg.AuthSentCode)
		if !ok {
			return fmt.Errorf("auth.sendCode returned %T", sent)
		}
		authorization, err := raw.AuthSignIn(ctx, &tg.AuthSignInRequest{
			PhoneNumber: record.Phone, PhoneCodeHash: sentCode.PhoneCodeHash, PhoneCode: cfg.Code,
		})
		if err != nil {
			return fmt.Errorf("auth.signIn: %w", err)
		}
		if authorized, ok := authorization.(*tg.AuthAuthorization); ok {
			user, ok = authorized.User.(*tg.User)
			if !ok {
				return fmt.Errorf("auth.signIn user is %T", authorized.User)
			}
			return nil
		}
		if _, ok := authorization.(*tg.AuthAuthorizationSignUpRequired); !ok {
			return fmt.Errorf("auth.signIn returned %T", authorization)
		}
		signedUp, err := raw.AuthSignUp(ctx, &tg.AuthSignUpRequest{
			PhoneNumber: record.Phone, PhoneCodeHash: sentCode.PhoneCodeHash, FirstName: record.FirstName,
		})
		if err != nil {
			return fmt.Errorf("auth.signUp: %w", err)
		}
		authorized, ok := signedUp.(*tg.AuthAuthorization)
		if !ok {
			return fmt.Errorf("auth.signUp returned %T", signedUp)
		}
		user, ok = authorized.User.(*tg.User)
		if !ok {
			return fmt.Errorf("auth.signUp user is %T", authorized.User)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, errors.New("provision completed without a user")
	}
	return user, nil
}

var _ session.Storage = (*EncryptedFileStorage)(nil)
