package store

import (
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
)

// GCS keeps the snapshot in a Cloud Storage object.
//
// This is what makes scaling to zero safe. Cloud Run shuts the instance down when
// nothing is calling, and a snapshot held only in memory would go with it, leaving
// the next caller — Apps Script, which cannot wait more than a minute — with nothing
// to read. An object survives that, and survives redeploys too.
type GCS struct {
	client *storage.Client
	bucket string
	object string
}

// NewGCS connects to Cloud Storage using the ambient service account credentials.
func NewGCS(ctx context.Context, bucket, object string) (*GCS, error) {
	if bucket == "" || object == "" {
		return nil, errors.New("bucket and object are both required")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting to cloud storage: %w", err)
	}
	return &GCS{client: client, bucket: bucket, object: object}, nil
}

// Load reads the stored object, returning ErrNotFound when it does not exist yet.
func (g *GCS) Load(ctx context.Context) ([]byte, error) {
	reader, err := g.client.Bucket(g.bucket).Object(g.object).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", g.Describe(), err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", g.Describe(), err)
	}
	return data, nil
}

// Save writes the object, replacing whatever was there.
func (g *GCS) Save(ctx context.Context, data []byte) error {
	writer := g.client.Bucket(g.bucket).Object(g.object).NewWriter(ctx)
	// The snapshot is already gzipped, so let clients receive it as-is rather than
	// having Cloud Storage decompress on the fly.
	writer.ContentType = "application/json"
	writer.ContentEncoding = "gzip"
	// A refresh is the only writer and Cloud Run runs a single instance, so there is
	// no read-modify-write to protect; a whole-object replace is the whole operation.
	writer.CacheControl = "no-store"

	if _, err := writer.Write(data); err != nil {
		// Close still has to be called to release the resumable upload.
		_ = writer.Close()
		return fmt.Errorf("writing %s: %w", g.Describe(), err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", g.Describe(), err)
	}
	return nil
}

// Close releases the Cloud Storage client.
func (g *GCS) Close() error { return g.client.Close() }

// Describe names the backing location.
func (g *GCS) Describe() string { return fmt.Sprintf("gs://%s/%s", g.bucket, g.object) }
