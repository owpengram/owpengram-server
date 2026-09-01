package postgres

import (
	"crypto/sha256"
	"encoding/hex"

	"telesrv/internal/domain"
)

func postgresTestBlob(locationKey, label string, size int64, mimeType string) domain.FileBlob {
	data := make([]byte, size)
	seed := sha256.Sum256([]byte(label))
	for i := range data {
		data[i] = seed[i%len(seed)]
	}
	digest := sha256.Sum256(data)
	return domain.FileBlob{
		LocationKey: locationKey,
		Backend:     domain.MediaBackendLocalFS,
		ObjectKey:   hex.EncodeToString(digest[:]),
		Size:        size,
		SHA256:      append([]byte(nil), digest[:]...),
		MimeType:    mimeType,
	}
}
