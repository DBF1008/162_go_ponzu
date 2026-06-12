title: Storing uploads in S3-compatible object storage

By default, Ponzu writes uploaded files (from the admin **Uploads** view, from
content that includes file fields, and from the API content endpoints) to the
local filesystem under the upload directory. This is simple and fast, but in a
**multi-instance deployment** or on a host with an **ephemeral disk**, a file
written by one instance is not visible to the others (and may be lost on
restart).

To solve this, Ponzu can optionally store uploads in an **S3-compatible object
storage** service — AWS S3, MinIO, DigitalOcean Spaces, Wasabi, Backblaze B2,
and similar. When configured, every upload path (admin, content, and API) writes
to the bucket and records a stable, externally accessible URL. When it is **not**
configured, Ponzu transparently falls back to local filesystem storage, so
existing single-instance deployments need no changes.

The backend is selected automatically at runtime from environment variables —
there is nothing to toggle in the admin UI.

---

#### Enabling object storage

Object storage is enabled as soon as the three required credentials are present
in the environment:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PONZU_S3_BUCKET` | **yes** | — | Bucket (or "space") name. |
| `PONZU_S3_ACCESS_KEY_ID` | **yes** | — | Access key ID. |
| `PONZU_S3_SECRET_ACCESS_KEY` | **yes** | — | Secret access key. |
| `PONZU_S3_REGION` | no | `us-east-1` | Region used for request signing. |
| `PONZU_S3_ENDPOINT` | no | `https://s3.<region>.amazonaws.com` | Service endpoint. Set this for non-AWS S3-compatible services. |
| `PONZU_S3_PUBLIC_BASE_URL` | no | `<endpoint>/<bucket>` | Base URL used to build the public file URL. Set this to a CDN/custom domain. |
| `PONZU_S3_PREFIX` | no | `uploads` | Key prefix for stored objects. |
| `PONZU_S3_ACL` | no | `public-read` | Canned ACL applied on upload. Use `none` to omit the ACL header. |

If any of the three required variables is missing, Ponzu uses local storage.

!!! note "Making files accessible"
    The URL recorded for each upload must be reachable by the clients that
    consume your content. The default `public-read` ACL makes objects publicly
    readable; if you front the bucket with a CDN or use a custom domain, set
    `PONZU_S3_PUBLIC_BASE_URL` so the recorded URLs point there. Objects are
    uploaded over HTTPS and signed with AWS Signature Version 4.

---

#### Example: AWS S3

```bash
export PONZU_S3_BUCKET=my-ponzu-uploads
export PONZU_S3_REGION=us-east-1
export PONZU_S3_ACCESS_KEY_ID=AKIA...
export PONZU_S3_SECRET_ACCESS_KEY=...
# optional: serve via CloudFront / a custom domain
export PONZU_S3_PUBLIC_BASE_URL=https://cdn.example.com

ponzu run --port=80
```

An upload is then stored at `s3://my-ponzu-uploads/uploads/<YYYY>/<MM>/<file>`
and referenced as `https://cdn.example.com/uploads/<YYYY>/<MM>/<file>`.

---

#### Example: MinIO or another S3-compatible service

Set `PONZU_S3_ENDPOINT` to the service URL (path-style addressing is used):

```bash
export PONZU_S3_ENDPOINT=https://minio.example.com
export PONZU_S3_BUCKET=ponzu-uploads
export PONZU_S3_REGION=us-east-1
export PONZU_S3_ACCESS_KEY_ID=minioadmin
export PONZU_S3_SECRET_ACCESS_KEY=minioadmin
export PONZU_S3_PUBLIC_BASE_URL=https://minio.example.com/ponzu-uploads

ponzu run --port=80
```
