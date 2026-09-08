package files

import (
	"context"
	"io"
	"time"
)

// GuardedBlobBackend applies one capacity policy to every permanent write,
// including seeds and non-upload media paths, instead of relying on individual
// RPC handlers to remember a check.
type GuardedBlobBackend struct {
	backend BlobBackend
	guard   SpaceGuard
}

func NewGuardedBlobBackend(backend BlobBackend, guard SpaceGuard) *GuardedBlobBackend {
	return &GuardedBlobBackend{backend: backend, guard: guard}
}

func (g *GuardedBlobBackend) Name() string { return g.backend.Name() }

func (g *GuardedBlobBackend) Put(ctx context.Context, data []byte) (string, error) {
	if err := requireSpace(g.guard, int64(len(data))); err != nil {
		return "", err
	}
	return g.backend.Put(ctx, data)
}

func (g *GuardedBlobBackend) PutReader(ctx context.Context, r io.Reader) (string, int64, []byte, error) {
	return g.backend.PutReader(ctx, &capacityReader{src: r, guard: g.guard})
}

func (g *GuardedBlobBackend) Get(ctx context.Context, key string) ([]byte, error) {
	return g.backend.Get(ctx, key)
}

func (g *GuardedBlobBackend) GetRange(ctx context.Context, key string, offset, limit int64) ([]byte, int64, error) {
	return g.backend.GetRange(ctx, key, offset, limit)
}

type GuardedUploadPartBackend struct {
	backend UploadPartBackend
	guard   SpaceGuard
}

func NewGuardedUploadPartBackend(backend UploadPartBackend, guard SpaceGuard) *GuardedUploadPartBackend {
	return &GuardedUploadPartBackend{backend: backend, guard: guard}
}

func (g *GuardedUploadPartBackend) PutUploadPart(ctx context.Context, ownerUserID, fileID int64, part int, data []byte) (uploadPartObject, error) {
	if err := requireSpace(g.guard, int64(len(data))); err != nil {
		return uploadPartObject{}, err
	}
	return g.backend.PutUploadPart(ctx, ownerUserID, fileID, part, data)
}

func (g *GuardedUploadPartBackend) GetUploadPart(ctx context.Context, key string) ([]byte, error) {
	return g.backend.GetUploadPart(ctx, key)
}

func (g *GuardedUploadPartBackend) OpenUploadPart(ctx context.Context, key string) (io.ReadCloser, error) {
	return g.backend.OpenUploadPart(ctx, key)
}

func (g *GuardedUploadPartBackend) DeleteUploadPart(ctx context.Context, key string) error {
	return g.backend.DeleteUploadPart(ctx, key)
}

func (g *GuardedUploadPartBackend) DeleteExpiredUploadParts(ctx context.Context, before time.Time, limit int) (int64, error) {
	return g.backend.DeleteExpiredUploadParts(ctx, before, limit)
}
