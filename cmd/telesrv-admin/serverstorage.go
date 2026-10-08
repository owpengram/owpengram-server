package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"telesrv/internal/config"
)

// requestShutdown makes this process exit cleanly; telesrv-ctl starts it
// again. Set by run(). It is how the panel "restarts itself" after a change
// the new process has to read from .env (storage, update).
var requestShutdown = func() {}

// exitSoon lets the HTTP response that triggered the restart reach the
// browser before the process goes away.
func exitSoon() {
	go func() {
		time.Sleep(700 * time.Millisecond)
		requestShutdown()
	}()
}

// storageSettings is what the first-run wizard's storage step shows and
// saves: where PostgreSQL lives and where uploaded files are kept. The S3
// secret is never sent back to the browser.
type storageSettings struct {
	PostgresMode string `json:"postgres_mode"`
	// PostgresDSN is the connection string with the password blanked, for
	// display. A save with the password left out keeps the stored one.
	PostgresDSN       string `json:"postgres_dsn"`
	EmbeddedDir       string `json:"embedded_dir"`
	BlobBackend       string `json:"blob_backend"`
	BlobDir           string `json:"blob_dir"`
	S3Endpoint        string `json:"s3_endpoint"`
	S3Bucket          string `json:"s3_bucket"`
	S3Region          string `json:"s3_region"`
	S3AccessKeyID     string `json:"s3_access_key_id"`
	S3SecretAccessKey string `json:"s3_secret_access_key,omitempty"`
	S3SecretSet       bool   `json:"s3_secret_set"`
	S3UseSSL          bool   `json:"s3_use_ssl"`
	S3PathStyle       bool   `json:"s3_path_style"`
	S3CreateBucket    bool   `json:"s3_create_bucket"`
	// Configured is false until the first save (or on an older install, which
	// counts as configured from the start).
	Configured bool `json:"configured"`
}

const (
	defaultEmbeddedDir = "data/postgres"
	defaultBlobDir     = "data/blobs"
)

func (s *server) currentStorage() storageSettings {
	env := func(key, def string) string {
		if v := s.serverCtl.EnvValue(key); v != "" {
			return v
		}
		return def
	}
	dsn := s.serverCtl.EnvValue("TELESRV_POSTGRES_DSN")
	mode := config.ResolvePostgresMode(s.serverCtl.EnvValue("TELESRV_POSTGRES_MODE"), s.serverCtl.EnvValue("TELESRV_EDITION"), dsn)
	backend := strings.ToLower(s.serverCtl.EnvValue("TELESRV_BLOB_BACKEND"))
	if backend == "" {
		backend = "localfs"
		if s.serverCtl.EnvValue("TELESRV_S3_ENDPOINT") != "" {
			backend = "s3"
		}
	}
	return storageSettings{
		PostgresMode:   mode,
		PostgresDSN:    maskDSNPassword(dsn),
		EmbeddedDir:    env("TELESRV_EMBEDDED_POSTGRES_DIR", defaultEmbeddedDir),
		BlobBackend:    backend,
		BlobDir:        env("TELESRV_BLOB_DIR", defaultBlobDir),
		S3Endpoint:     s.serverCtl.EnvValue("TELESRV_S3_ENDPOINT"),
		S3Bucket:       env("TELESRV_S3_BUCKET", "owpengram-media"),
		S3Region:       env("TELESRV_S3_REGION", "us-east-1"),
		S3AccessKeyID:  s.serverCtl.EnvValue("TELESRV_S3_ACCESS_KEY_ID"),
		S3SecretSet:    s.serverCtl.EnvValue("TELESRV_S3_SECRET_ACCESS_KEY") != "",
		S3UseSSL:       strings.EqualFold(s.serverCtl.EnvValue("TELESRV_S3_USE_SSL"), "true"),
		S3PathStyle:    !strings.EqualFold(s.serverCtl.EnvValue("TELESRV_S3_PATH_STYLE"), "false"),
		S3CreateBucket: strings.EqualFold(s.serverCtl.EnvValue("TELESRV_S3_CREATE_BUCKET"), "true"),
		Configured:     s.serverCtl.StorageConfigured(),
	}
}

func maskDSNPassword(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "****")
	}
	return u.String()
}

func (s *server) handleServerStorageAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentStorage())
}

type configureStorageAPIRequest struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Confirm   bool   `json:"confirm"`
	storageSettings
}

// handleConfigureStorageAPI saves the storage choice to .env and restarts the
// panel; telesrv-ctl then starts the server (and the embedded PostgreSQL)
// against it. Everything is checked first -- the database answers, the folder
// is writable, the bucket exists -- so a typo is reported here instead of as a
// server that will not start.
func (s *server) handleConfigureStorageAPI(w http.ResponseWriter, r *http.Request) {
	var body configureStorageAPIRequest
	if !decodeAction(w, r, &body) {
		return
	}
	meta := s.commandMetaFromAPI(r, body.CommandID, body.Reason, body.Confirm, "configure-storage")

	values, err := s.storageEnvValues(r.Context(), body.storageSettings)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	details := map[string]any{"postgres_mode": body.PostgresMode, "blob_backend": body.BlobBackend}
	if meta.DryRun {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.configure_storage", nil, "storage settings checked", details))
		return
	}
	if err := s.serverCtl.WriteEnvValues(values); err != nil {
		writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.configure_storage", err, "", details))
		return
	}
	writeJSON(w, http.StatusOK, serverCommandResult(meta, "server.configure_storage", nil, "storage saved -- the server is starting", details))
	exitSoon()
}

// storageEnvValues validates in and returns the .env keys that express it.
func (s *server) storageEnvValues(ctx context.Context, in storageSettings) (map[string]string, error) {
	values := map[string]string{
		// A save of the new keys retires the old edition switch.
		"TELESRV_EDITION": "",
	}

	switch in.PostgresMode {
	case config.PostgresModeEmbedded:
		dir := strings.TrimSpace(in.EmbeddedDir)
		if dir == "" {
			dir = defaultEmbeddedDir
		}
		values["TELESRV_POSTGRES_MODE"] = config.PostgresModeEmbedded
		values["TELESRV_EMBEDDED_POSTGRES_DIR"] = dir
		values["TELESRV_POSTGRES_DSN"] = ""
	case config.PostgresModeExternal:
		dsn := strings.TrimSpace(in.PostgresDSN)
		if dsn == "" {
			return nil, errors.New("enter the PostgreSQL connection string")
		}
		// The form shows the stored DSN with the password blanked; saving it
		// untouched must keep the real one.
		if strings.Contains(dsn, "****") {
			stored := s.serverCtl.EnvValue("TELESRV_POSTGRES_DSN")
			if maskDSNPassword(stored) != dsn {
				return nil, errors.New("the connection string has no password -- type the full string")
			}
			dsn = stored
		}
		if err := checkPostgres(ctx, dsn); err != nil {
			return nil, fmt.Errorf("cannot connect to PostgreSQL: %w", err)
		}
		values["TELESRV_POSTGRES_MODE"] = config.PostgresModeExternal
		values["TELESRV_POSTGRES_DSN"] = dsn
	default:
		return nil, fmt.Errorf("choose embedded or external PostgreSQL")
	}

	switch in.BlobBackend {
	case "localfs":
		dir := strings.TrimSpace(in.BlobDir)
		if dir == "" {
			dir = defaultBlobDir
		}
		if err := checkWritableDir(s.cfg.RepoRoot, dir); err != nil {
			return nil, fmt.Errorf("cannot use the media folder: %w", err)
		}
		values["TELESRV_BLOB_BACKEND"] = "localfs"
		values["TELESRV_BLOB_DIR"] = dir
	case "s3":
		secret := in.S3SecretAccessKey
		if secret == "" {
			secret = s.serverCtl.EnvValue("TELESRV_S3_SECRET_ACCESS_KEY")
		}
		endpoint := strings.TrimSpace(in.S3Endpoint)
		bucket := strings.TrimSpace(in.S3Bucket)
		access := strings.TrimSpace(in.S3AccessKeyID)
		region := strings.TrimSpace(in.S3Region)
		if region == "" {
			region = "us-east-1"
		}
		switch {
		case endpoint == "" || strings.Contains(endpoint, "://"):
			return nil, errors.New("the S3 endpoint is host or host:port, without http://")
		case bucket == "":
			return nil, errors.New("enter the S3 bucket name")
		case access == "" || secret == "":
			return nil, errors.New("enter the S3 access key and secret key")
		}
		if err := checkS3(ctx, endpoint, bucket, access, secret, region, in.S3UseSSL, in.S3PathStyle, in.S3CreateBucket); err != nil {
			return nil, fmt.Errorf("cannot use the S3 bucket: %w", err)
		}
		values["TELESRV_BLOB_BACKEND"] = "s3"
		values["TELESRV_S3_ENDPOINT"] = endpoint
		values["TELESRV_S3_BUCKET"] = bucket
		values["TELESRV_S3_REGION"] = region
		values["TELESRV_S3_ACCESS_KEY_ID"] = access
		values["TELESRV_S3_SECRET_ACCESS_KEY"] = secret
		values["TELESRV_S3_USE_SSL"] = strconv.FormatBool(in.S3UseSSL)
		values["TELESRV_S3_PATH_STYLE"] = strconv.FormatBool(in.S3PathStyle)
		values["TELESRV_S3_CREATE_BUCKET"] = strconv.FormatBool(in.S3CreateBucket)
	default:
		return nil, fmt.Errorf("choose local disk or S3 for media")
	}
	return values, nil
}

func checkPostgres(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	return conn.Ping(ctx)
}

func checkWritableDir(root, dir string) error {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}

func checkS3(ctx context.Context, endpoint, bucket, access, secret, region string, ssl, pathStyle, create bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lookup := minio.BucketLookupAuto
	if pathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(access, secret, ""),
		Secure:       ssl,
		Region:       region,
		BucketLookup: lookup,
	})
	if err != nil {
		return err
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if !create {
		return fmt.Errorf("bucket %q does not exist (tick \"create the bucket\" to have it made)", bucket)
	}
	return nil
}

// serverListening reports whether owpengram-server accepts connections on its
// own port -- it only starts listening once startup (migrations, seeding) is
// done, so this doubles as "ready".
func (s *server) serverListening() bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.cfg.ServerPort)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
