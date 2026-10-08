package config

import "testing"

func TestResolvePostgresMode(t *testing.T) {
	tests := []struct {
		name, mode, edition, dsn, want string
	}{
		{"explicit embedded wins over a DSN", "embedded", "", "postgres://x", PostgresModeEmbedded},
		{"explicit external", "external", "", "", PostgresModeExternal},
		{"mode is case-insensitive", " External ", "", "", PostgresModeExternal},
		{"legacy portable", "", "portable", "postgres://x", PostgresModeEmbedded},
		{"legacy standard has no say", "", "standard", "", PostgresModeEmbedded},
		{"dsn without mode keeps an old docker install external", "", "", "postgres://owpengram@127.0.0.1:5432/owpengram", PostgresModeExternal},
		{"nothing configured is a fresh install", "", "", "", PostgresModeEmbedded},
	}
	for _, tt := range tests {
		if got := ResolvePostgresMode(tt.mode, tt.edition, tt.dsn); got != tt.want {
			t.Errorf("%s: ResolvePostgresMode(%q, %q, %q) = %q, want %q", tt.name, tt.mode, tt.edition, tt.dsn, got, tt.want)
		}
	}
}

// An .env written before the mode existed must keep pointing at the same
// PostgreSQL and the same blob store.
func TestLoadKeepsOldDockerInstallExternal(t *testing.T) {
	disableDefaultConfigFile(t)
	const dsn = "postgres://owpengram:owpengram@127.0.0.1:5432/owpengram?sslmode=disable"
	t.Setenv("TELESRV_POSTGRES_MODE", "")
	t.Setenv("TELESRV_EDITION", "")
	t.Setenv("TELESRV_POSTGRES_DSN", dsn)
	t.Setenv("TELESRV_BLOB_BACKEND", "s3")
	t.Setenv("TELESRV_S3_ENDPOINT", "127.0.0.1:9000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PostgresMode != PostgresModeExternal || cfg.PostgresDSN != dsn {
		t.Fatalf("mode=%q dsn=%q, want external with the configured DSN", cfg.PostgresMode, cfg.PostgresDSN)
	}
	if cfg.BlobBackendKind != "s3" || cfg.S3Endpoint != "127.0.0.1:9000" {
		t.Fatalf("backend=%q endpoint=%q, want the configured MinIO", cfg.BlobBackendKind, cfg.S3Endpoint)
	}
}

func TestLoadEmbeddedIgnoresDSN(t *testing.T) {
	disableDefaultConfigFile(t)
	t.Setenv("TELESRV_POSTGRES_MODE", "embedded")
	t.Setenv("TELESRV_POSTGRES_DSN", "postgres://elsewhere/db")
	t.Setenv("TELESRV_EMBEDDED_POSTGRES_PORT", "15555")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "postgres://owpengram:owpengram@127.0.0.1:15555/owpengram?sslmode=disable"; cfg.PostgresDSN != want {
		t.Fatalf("DSN = %q, want %q", cfg.PostgresDSN, want)
	}
}

func TestLoadBlobBackendFollowsWhatIsConfigured(t *testing.T) {
	for _, tt := range []struct {
		name, backend, endpoint, want string
	}{
		{"nothing configured is local disk", "", "", "localfs"},
		{"an endpoint without a backend is s3", "", "minio.example:9000", "s3"},
		{"explicit localfs beats an endpoint", "localfs", "minio.example:9000", "localfs"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			disableDefaultConfigFile(t)
			t.Setenv("TELESRV_BLOB_BACKEND", tt.backend)
			t.Setenv("TELESRV_S3_ENDPOINT", tt.endpoint)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.BlobBackendKind != tt.want {
				t.Fatalf("backend = %q, want %q", cfg.BlobBackendKind, tt.want)
			}
		})
	}
}

func TestLoadRejectsUnknownPostgresMode(t *testing.T) {
	disableDefaultConfigFile(t)
	t.Setenv("TELESRV_POSTGRES_MODE", "docker")
	if _, err := Load(); err == nil {
		t.Fatal("unknown TELESRV_POSTGRES_MODE accepted")
	}
}
