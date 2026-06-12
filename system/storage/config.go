// Package storage provides a pluggable file storage backend for Ponzu uploads.
// It supports local disk storage (default) and S3-compatible object storage.
// The backend is selected at runtime based on environment variables: when S3
// credentials are configured, files are written to object storage; otherwise
// they are written to the local filesystem as before.
package storage

import "os"

// Config holds S3-compatible object storage configuration, populated from
// environment variables. All fields except Endpoint are required for S3 mode.
type Config struct {
	Bucket    string // PONZU_S3_BUCKET
	Region    string // PONZU_S3_REGION
	AccessKey string // PONZU_S3_ACCESS_KEY
	SecretKey string // PONZU_S3_SECRET_KEY
	Endpoint  string // PONZU_S3_ENDPOINT (optional, for MinIO / R2 / Spaces)
}

// ConfigFromEnv reads S3 configuration from environment variables.
func ConfigFromEnv() Config {
	return Config{
		Bucket:    os.Getenv("PONZU_S3_BUCKET"),
		Region:    os.Getenv("PONZU_S3_REGION"),
		AccessKey: os.Getenv("PONZU_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("PONZU_S3_SECRET_KEY"),
		Endpoint:  os.Getenv("PONZU_S3_ENDPOINT"),
	}
}

// IsConfigured reports whether all required S3 environment variables are set.
func (c Config) IsConfigured() bool {
	return c.Bucket != "" && c.Region != "" && c.AccessKey != "" && c.SecretKey != ""
}
