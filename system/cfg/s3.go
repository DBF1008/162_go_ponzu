package cfg

import (
	"os"
	"strings"
)

// This file holds configuration for the optional S3-compatible object storage
// backend used by the upload system. When the required credentials are not
// present in the environment, the upload system falls back to local disk
// storage (see S3Configured).
//
// All settings are read from the environment so they can be supplied in
// multi-instance or ephemeral-disk deployments without code changes:
//
//	PONZU_S3_BUCKET             - bucket/space name (required to enable S3)
//	PONZU_S3_ACCESS_KEY_ID      - access key id (required to enable S3)
//	PONZU_S3_SECRET_ACCESS_KEY  - secret access key (required to enable S3)
//	PONZU_S3_REGION             - region, default "us-east-1"
//	PONZU_S3_ENDPOINT           - custom endpoint for S3-compatible services
//	                             (e.g. https://nyc3.digitaloceanspaces.com or a
//	                             MinIO URL). Defaults to the AWS regional host.
//	PONZU_S3_PUBLIC_BASE_URL    - base URL used to build the returned public
//	                             file address (e.g. a CDN domain). Defaults to
//	                             "<endpoint>/<bucket>".
//	PONZU_S3_PREFIX             - key prefix for uploaded objects, default "uploads"
//	PONZU_S3_ACL                - canned ACL applied on upload, default
//	                             "public-read"; set to "none" to omit it.

const (
	defaultS3Region = "us-east-1"
	defaultS3Prefix = "uploads"
	defaultS3ACL    = "public-read"
)

// S3Bucket returns the configured object storage bucket name.
func S3Bucket() string {
	return strings.TrimSpace(os.Getenv("PONZU_S3_BUCKET"))
}

// S3AccessKeyID returns the configured object storage access key id.
func S3AccessKeyID() string {
	return strings.TrimSpace(os.Getenv("PONZU_S3_ACCESS_KEY_ID"))
}

// S3SecretAccessKey returns the configured object storage secret access key.
func S3SecretAccessKey() string {
	return strings.TrimSpace(os.Getenv("PONZU_S3_SECRET_ACCESS_KEY"))
}

// S3Region returns the configured region, defaulting to us-east-1.
func S3Region() string {
	region := strings.TrimSpace(os.Getenv("PONZU_S3_REGION"))
	if region == "" {
		return defaultS3Region
	}
	return region
}

// S3Endpoint returns the base endpoint for the object storage service. When not
// explicitly set it defaults to the AWS regional S3 endpoint so plain AWS S3
// works without additional configuration.
func S3Endpoint() string {
	endpoint := strings.TrimSpace(os.Getenv("PONZU_S3_ENDPOINT"))
	if endpoint == "" {
		return "https://s3." + S3Region() + ".amazonaws.com"
	}
	return strings.TrimRight(endpoint, "/")
}

// S3PublicBaseURL returns the base URL used to construct the publicly accessible
// address of an uploaded object. Empty means "derive from endpoint + bucket".
func S3PublicBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PONZU_S3_PUBLIC_BASE_URL")), "/")
}

// S3Prefix returns the key prefix applied to uploaded objects (default "uploads").
func S3Prefix() string {
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("PONZU_S3_PREFIX")), "/")
	if prefix == "" {
		return defaultS3Prefix
	}
	return prefix
}

// S3ACL returns the canned ACL to apply when uploading objects. Defaults to
// "public-read" so returned URLs are accessible; "none" omits the header.
func S3ACL() string {
	acl, ok := os.LookupEnv("PONZU_S3_ACL")
	if !ok {
		return defaultS3ACL
	}
	return strings.TrimSpace(acl)
}

// S3Configured reports whether enough credentials are present to use the
// S3-compatible object storage backend. When false, callers fall back to local
// filesystem storage.
func S3Configured() bool {
	return S3Bucket() != "" && S3AccessKeyID() != "" && S3SecretAccessKey() != ""
}
