package loadharness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owpengram/owpengram-td/tg"
)

const (
	fileFixtureVersion    = 1
	fixturePatternVersion = 1
)

// persistedFileFixture keeps only the stable location of a synthetic load-test
// document. It contains no auth key or login secret and is owner-readable so a
// test bundle can reuse the same server-side file across independent runs.
type persistedFileFixture struct {
	Version        int       `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	ServerAddress  string    `json:"server_address"`
	DC             int       `json:"dc"`
	SizeBytes      int       `json:"size_bytes"`
	PatternVersion int       `json:"pattern_version"`
	DocumentID     int64     `json:"document_id"`
	AccessHash     int64     `json:"access_hash"`
	FileReference  []byte    `json:"file_reference"`
}

func (f *persistedFileFixture) validate(endpoint Endpoint, size int) error {
	if f == nil {
		return errors.New("nil file fixture")
	}
	if f.Version != fileFixtureVersion || f.PatternVersion != fixturePatternVersion {
		return errors.New("file fixture version does not match the harness")
	}
	if f.ServerAddress != endpoint.Address || f.DC != endpoint.DC {
		return errors.New("file fixture endpoint does not match the manifest")
	}
	if f.SizeBytes != size || f.SizeBytes <= 0 {
		return fmt.Errorf("file fixture size %d does not match requested %d", f.SizeBytes, size)
	}
	if f.DocumentID == 0 || f.AccessHash == 0 || len(f.FileReference) == 0 {
		return errors.New("file fixture has an incomplete document location")
	}
	return nil
}

func (f *persistedFileFixture) runtime(chunk int) *downloadFixture {
	return &downloadFixture{
		location: &tg.InputDocumentFileLocation{
			ID: f.DocumentID, AccessHash: f.AccessHash,
			FileReference: append([]byte(nil), f.FileReference...),
		},
		size: f.SizeBytes, chunk: chunk,
	}
}

func persistedFixture(endpoint Endpoint, fixture *downloadFixture) *persistedFileFixture {
	return &persistedFileFixture{
		Version: fileFixtureVersion, CreatedAt: time.Now().UTC(),
		ServerAddress: endpoint.Address, DC: endpoint.DC,
		SizeBytes: fixture.size, PatternVersion: fixturePatternVersion,
		DocumentID: fixture.location.ID, AccessHash: fixture.location.AccessHash,
		FileReference: append([]byte(nil), fixture.location.FileReference...),
	}
}

func resolveFileFixturePath(manifestPath, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(filepath.Dir(manifestPath), "file-fixture.json")
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(filepath.Dir(manifestPath), filepath.FromSlash(configured))
}

func loadPersistedFileFixture(path string, endpoint Endpoint, size, chunk int) (*downloadFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fixture persistedFileFixture
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("decode file fixture: %w", err)
	}
	if err := fixture.validate(endpoint, size); err != nil {
		return nil, err
	}
	return fixture.runtime(chunk), nil
}

func writePersistedFileFixture(path string, endpoint Endpoint, fixture *downloadFixture) error {
	persisted := persistedFixture(endpoint, fixture)
	if err := persisted.validate(endpoint, fixture.size); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode file fixture: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}
