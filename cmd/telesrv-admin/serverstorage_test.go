package main

import (
	"context"
	"strings"
	"testing"

	"telesrv/internal/procctl"
)

func newStorageTestServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	return &server{cfg: uiConfig{RepoRoot: dir}, serverCtl: procctl.NewManager(dir)}
}

func TestStorageEnvValuesEmbeddedLocalDisk(t *testing.T) {
	s := newStorageTestServer(t)
	values, err := s.storageEnvValues(context.Background(), storageSettings{PostgresMode: "embedded", BlobBackend: "localfs"})
	if err != nil {
		t.Fatalf("storageEnvValues: %v", err)
	}
	for key, want := range map[string]string{
		"TELESRV_POSTGRES_MODE":         "embedded",
		"TELESRV_EMBEDDED_POSTGRES_DIR": "data/postgres",
		"TELESRV_POSTGRES_DSN":          "",
		"TELESRV_BLOB_BACKEND":          "localfs",
		"TELESRV_BLOB_DIR":              "data/blobs",
	} {
		if got, ok := values[key]; !ok || got != want {
			t.Errorf("%s = %q (set=%v), want %q", key, got, ok, want)
		}
	}
}

func TestStorageEnvValuesRejectsIncompleteChoices(t *testing.T) {
	s := newStorageTestServer(t)
	for name, in := range map[string]storageSettings{
		"no database choice":    {BlobBackend: "localfs"},
		"external without dsn":  {PostgresMode: "external", BlobBackend: "localfs"},
		"no media choice":       {PostgresMode: "embedded"},
		"s3 endpoint scheme":    {PostgresMode: "embedded", BlobBackend: "s3", S3Endpoint: "http://minio:9000", S3Bucket: "b", S3AccessKeyID: "a", S3SecretAccessKey: "s"},
		"s3 without bucket":     {PostgresMode: "embedded", BlobBackend: "s3", S3Endpoint: "minio:9000", S3AccessKeyID: "a", S3SecretAccessKey: "s"},
		"s3 without credential": {PostgresMode: "embedded", BlobBackend: "s3", S3Endpoint: "minio:9000", S3Bucket: "b"},
	} {
		if _, err := s.storageEnvValues(context.Background(), in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMaskDSNPassword(t *testing.T) {
	got := maskDSNPassword("postgres://owpengram:secret@127.0.0.1:5432/owpengram?sslmode=disable")
	if strings.Contains(got, "secret") || !strings.Contains(got, "owpengram") {
		t.Fatalf("masked DSN = %q", got)
	}
}
