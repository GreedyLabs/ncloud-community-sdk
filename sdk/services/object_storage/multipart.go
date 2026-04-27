// Multipart upload helpers for NCP Object Storage.
//
// Plain `PutObjectWithBody(...)` requires the entire body in memory because
// AWS SigV4 needs the payload SHA-256 up front (and the SDK's hand-rolled
// signer does not implement the chunked-upload `STREAMING-AWS4-HMAC-SHA256-
// PAYLOAD` flow). For objects larger than ~5 MiB the practical solution is
// the standard S3 multipart-upload protocol — Initiate, Upload-Part × N,
// Complete — which signs each part independently with a known SHA-256.
//
// `UploadLargeObject` wraps that 3-step flow so callers can hand it any
// `io.Reader` of unknown total size and get back the final ETag without
// touching `InitiateMultipartUpload` / `UploadPart` / `CompleteMultipartUpload`
// directly. On any error after Initiate, the helper calls
// AbortMultipartUpload so a failure does not leave orphaned parts billing
// against the bucket.

package object_storage

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// MultipartUploadOptions tunes UploadLargeObject. Zero values pick safe
// defaults — 5 MiB parts (S3 minimum), single-threaded sequential upload,
// no per-call ContentType override.
type MultipartUploadOptions struct {
	// PartSize is the byte size of each non-final part. Must be >= 5 MiB
	// (S3 protocol minimum) and small enough to fit in memory; the helper
	// holds one PartSize buffer in memory at a time. Zero defaults to
	// MinPartSize.
	PartSize int

	// ContentType is set as the object's Content-Type when the multipart
	// upload completes. Empty leaves the server-derived default.
	ContentType string

	// CannedACL is set on the upload via x-amz-acl. Empty omits the
	// header (uses the bucket default).
	CannedACL string

	// ExtraInitiateHeaders are passed to InitiateMultipartUpload via a
	// RequestEditorFn. Use this to apply x-amz-meta-* metadata, storage
	// class, encryption headers, etc.
	ExtraInitiateHeaders map[string]string

	// Concurrency is the number of parts to upload in parallel. Zero or
	// 1 keeps the original sequential behaviour (one read, one upload,
	// repeat). Set to e.g. 4 to overlap N uploads — peak memory grows
	// linearly with this value (Concurrency × PartSize) since each in-
	// flight part holds its buffer until its UploadPart call returns.
	//
	// Producer / consumer ordering is preserved internally: parts are
	// read in PartNumber order and the final CompleteMultipartUpload
	// receives the (PartNumber, ETag) pairs in PartNumber order, even
	// though the wire-level uploads complete in arrival order.
	Concurrency int
}

// MinPartSize is the S3 protocol minimum for multipart parts (except the
// last one). Set the same way AWS documents it — 5 MiB.
const MinPartSize = 5 * 1024 * 1024

// MaxPartCount caps the number of parts per upload. The S3 protocol
// allows up to 10,000.
const MaxPartCount = 10000

// UploadLargeObject performs an S3 multipart upload of `body` to
// `bucket`/`key`, automatically initiating, uploading parts, and
// completing the upload. On any error after Initiate it issues
// AbortMultipartUpload so the in-progress parts do not linger on the
// bucket.
//
// The returned ETag is the multipart-style ETag of the assembled object
// (a hex MD5 of the concatenated part MD5s plus "-N" where N is the part
// count). Callers should not compare it byte-for-byte with the MD5 of
// the original body — that's not how multipart ETags work in S3.
//
// The helper reads `body` strictly forward; it is safe to pass an
// os.File / *os.Stdin / network stream. If the reader produces less than
// one PartSize total, only one part is uploaded.
func (c *ClientWithResponses) UploadLargeObject(
	ctx context.Context,
	bucket, key string,
	body io.Reader,
	opts MultipartUploadOptions,
) (string, error) {
	if bucket == "" {
		return "", errors.New("object_storage: UploadLargeObject: bucket is empty")
	}
	if key == "" {
		return "", errors.New("object_storage: UploadLargeObject: key is empty")
	}
	if body == nil {
		return "", errors.New("object_storage: UploadLargeObject: body is nil")
	}
	partSize := opts.PartSize
	if partSize == 0 {
		partSize = MinPartSize
	}
	if partSize < MinPartSize {
		return "", fmt.Errorf("object_storage: UploadLargeObject: PartSize=%d below the S3 minimum (%d)", partSize, MinPartSize)
	}

	// Step 1 — InitiateMultipartUpload. The server returns an UploadId we
	// then attach to every UploadPart call. Use the WithBody variant with
	// an empty body so codegen's optional-body branch is satisfied.
	uploadId, err := c.initiateUpload(ctx, bucket, key, opts)
	if err != nil {
		return "", fmt.Errorf("initiate: %w", err)
	}

	// Step 2 — stream-upload parts. Sequential when Concurrency<=1,
	// otherwise dispatch through the worker pool below. Either path
	// returns parts in PartNumber order so CompleteMultipartUpload can
	// consume them directly.
	var parts []CompletedPart
	var uploadErr error
	if opts.Concurrency <= 1 {
		parts, uploadErr = c.uploadAllParts(ctx, bucket, key, uploadId, partSize, body)
	} else {
		parts, uploadErr = c.uploadAllPartsConcurrent(ctx, bucket, key, uploadId, partSize, body, opts.Concurrency)
	}
	if uploadErr != nil {
		// Best-effort abort — surface the original error if both fail.
		_, _ = c.DeleteObjectWithResponse(ctx, bucket, key, &DeleteObjectParams{
			UploadId: &uploadId,
		})
		return "", fmt.Errorf("upload parts: %w", uploadErr)
	}
	if len(parts) == 0 {
		// Empty input — abort and return an empty-input error rather than
		// completing a 0-part upload (S3 rejects that anyway).
		_, _ = c.DeleteObjectWithResponse(ctx, bucket, key, &DeleteObjectParams{
			UploadId: &uploadId,
		})
		return "", errors.New("object_storage: UploadLargeObject: body produced zero bytes")
	}

	// Step 3 — CompleteMultipartUpload. Body is the part list in order.
	etag, err := c.completeUpload(ctx, bucket, key, uploadId, parts)
	if err != nil {
		_, _ = c.DeleteObjectWithResponse(ctx, bucket, key, &DeleteObjectParams{
			UploadId: &uploadId,
		})
		return "", fmt.Errorf("complete: %w", err)
	}
	return etag, nil
}

// initiateUpload calls InitiateMultipartUpload and returns the UploadId.
//
// The codegen op is `InitiateMultipartUploadWithBodyWithResponse` — it
// expects an XML body even when there is none, so we send an empty
// reader with the right content type.
func (c *ClientWithResponses) initiateUpload(
	ctx context.Context, bucket, key string, opts MultipartUploadOptions,
) (string, error) {
	editors := []RequestEditorFn{
		// `?uploads` (no value) is the discriminator that turns POST into
		// InitiateMultipartUpload. We rewrite RawQuery from scratch to
		// avoid the duplicate-key pollution introduced by codegen's path
		// template (see uploadOnePart's comment for the full diagnosis).
		func(_ context.Context, req *http.Request) error {
			req.URL.RawQuery = "uploads="
			if opts.ContentType != "" {
				req.Header.Set("Content-Type", opts.ContentType)
			}
			if opts.CannedACL != "" {
				req.Header.Set("x-amz-acl", opts.CannedACL)
			}
			for k, v := range opts.ExtraInitiateHeaders {
				req.Header.Set(k, v)
			}
			return nil
		},
	}

	resp, err := c.InitiateMultipartUploadWithBodyWithResponse(
		ctx, bucket, key,
		&InitiateMultipartUploadParams{}, // populated by editor; spec marks `uploads` as the trigger
		"application/xml",
		strings.NewReader(""),
		editors...,
	)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode(), bodyPreview(resp.Body))
	}
	// The init response is one of two shapes via oneOf — but the actual
	// server reply is always InitiateMultipartUploadResult. Parse it
	// directly to avoid depending on codegen's oneOf decoding.
	var out InitiateMultipartUploadResult
	if err := xml.Unmarshal(resp.Body, &out); err != nil {
		return "", fmt.Errorf("decode initiate response: %w", err)
	}
	if out.UploadId == nil || *out.UploadId == "" {
		return "", errors.New("server returned empty UploadId")
	}
	return *out.UploadId, nil
}

// uploadAllParts reads the body in PartSize chunks and uploads each as a
// part of the multipart upload. Returns the ordered (PartNumber, ETag)
// list ready to feed to CompleteMultipartUpload.
func (c *ClientWithResponses) uploadAllParts(
	ctx context.Context,
	bucket, key, uploadId string,
	partSize int,
	body io.Reader,
) ([]CompletedPart, error) {
	parts := make([]CompletedPart, 0, 16)
	buf := make([]byte, partSize)

	for partNum := 1; partNum <= MaxPartCount; partNum++ {
		// Read up to partSize bytes. ReadFull lets us detect a short read
		// (the final part) without ambiguity.
		n, err := io.ReadFull(body, buf)
		if n > 0 {
			etag, upErr := c.uploadOnePart(ctx, bucket, key, uploadId, partNum, buf[:n])
			if upErr != nil {
				return nil, fmt.Errorf("part %d: %w", partNum, upErr)
			}
			parts = append(parts, CompletedPart{PartNumber: partNum, ETag: etag})
		}
		switch {
		case err == nil:
			continue // full part read; loop for more
		case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
			return parts, nil // done — last part was short or zero
		default:
			return nil, fmt.Errorf("read part %d: %w", partNum, err)
		}
	}
	return nil, fmt.Errorf("body exceeds the S3 multipart limit of %d parts", MaxPartCount)
}

// uploadAllPartsConcurrent fans the part uploads across `workers`
// goroutines while preserving PartNumber order in the returned slice.
//
// Producer (this goroutine):
//   reads PartSize chunks in PartNumber order, pushes each (partNumber,
//   []byte) onto the jobs channel.
//
// Workers (goroutines × workers):
//   pop jobs, call uploadOnePart, push result.
//
// On first error: cancel ctx so any in-flight UploadPart calls return
// promptly. The caller still drives the eventual AbortMultipartUpload
// from the helper's error path. Memory ceiling is workers × PartSize.
func (c *ClientWithResponses) uploadAllPartsConcurrent(
	parentCtx context.Context,
	bucket, key, uploadId string,
	partSize int,
	body io.Reader,
	workers int,
) ([]CompletedPart, error) {
	type job struct {
		partNumber int
		body       []byte
	}
	type result struct {
		partNumber int
		etag       string
		err        error
	}

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	jobs := make(chan job, workers) // bounded so producer back-pressures on slow workers

	var wg sync.WaitGroup
	results := make(chan result, workers*2)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				etag, err := c.uploadOnePart(ctx, bucket, key, uploadId, j.partNumber, j.body)
				select {
				case results <- result{j.partNumber, etag, err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	// Closer: when all workers exit (jobs drained), close results so the
	// collector below stops blocking.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Producer — reads in PartNumber order.
	var producerErr error
	dispatched := 0
producerLoop:
	for partNum := 1; partNum <= MaxPartCount; partNum++ {
		buf := make([]byte, partSize)
		n, err := io.ReadFull(body, buf)
		if n > 0 {
			select {
			case jobs <- job{partNumber: partNum, body: buf[:n]}:
				dispatched++
			case <-ctx.Done():
				producerErr = ctx.Err()
				break producerLoop
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			break producerLoop
		default:
			producerErr = fmt.Errorf("read part %d: %w", partNum, err)
			break producerLoop
		}
	}
	close(jobs) // signals workers to drain remaining and exit

	// Collector — drains results channel until it's closed by the wg-
	// waiter goroutine. First error wins; cancel context to stop in-
	// flight peers fast.
	collected := make([]result, 0, dispatched)
	var firstErr error
	for r := range results {
		collected = append(collected, r)
		if r.err != nil && firstErr == nil {
			firstErr = r.err
			cancel()
		}
	}

	if producerErr != nil {
		return nil, producerErr
	}
	if firstErr != nil {
		return nil, firstErr
	}

	parts := make([]CompletedPart, 0, len(collected))
	for _, r := range collected {
		parts = append(parts, CompletedPart{PartNumber: r.partNumber, ETag: r.etag})
	}
	sortByPartNumber(parts)
	return parts, nil
}

// sortByPartNumber orders a slice of CompletedPart in ascending
// PartNumber. Insertion sort keeps the dependency footprint flat
// (no `sort` import) and is fast enough for typical part counts (≤ 10000).
func sortByPartNumber(parts []CompletedPart) {
	for i := 1; i < len(parts); i++ {
		for j := i; j > 0 && parts[j-1].PartNumber > parts[j].PartNumber; j-- {
			parts[j], parts[j-1] = parts[j-1], parts[j]
		}
	}
}

// uploadOnePart calls UploadPart for a single (partNumber, body) pair and
// returns the ETag the server assigned.
//
// Note: oapi-codegen's UploadPart wrapper builds the URL by literally
// embedding the path key — `/{bucket}/{object}?partNumber` — and then
// appending the params, which yields a duplicated `partNumber=` query
// segment. AWS SigV4 then signs the malformed canonical request and the
// server returns SignatureDoesNotMatch. We sidestep this by passing empty
// params to the codegen wrapper and rewriting RawQuery to the clean
// pair via a RequestEditorFn, which fires before the SigV4 transport
// signs the request.
func (c *ClientWithResponses) uploadOnePart(
	ctx context.Context,
	bucket, key, uploadId string,
	partNumber int,
	body []byte,
) (string, error) {
	cleanQuery := func(_ context.Context, req *http.Request) error {
		q := make(map[string][]string, 2)
		q["partNumber"] = []string{strconv.Itoa(partNumber)}
		q["uploadId"] = []string{uploadId}
		req.URL.RawQuery = encodeQuery(q)
		req.Header.Set("Content-Length", strconv.FormatInt(int64(len(body)), 10))
		return nil
	}
	resp, err := c.UploadPartWithBodyWithResponse(ctx, bucket, key,
		&UploadPartParams{
			PartNumber:    partNumber, // kept for godoc clarity; editor overrides URL
			UploadId:      uploadId,
			ContentLength: int64(len(body)),
		},
		"application/octet-stream",
		strings.NewReader(string(body)),
		cleanQuery,
	)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode(), bodyPreview(resp.Body))
	}
	if resp.HTTPResponse == nil {
		return "", errors.New("UploadPart: nil HTTPResponse")
	}
	etag := resp.HTTPResponse.Header.Get("ETag")
	if etag == "" {
		return "", errors.New("UploadPart: server returned empty ETag")
	}
	return etag, nil
}

// encodeQuery sorts and url-encodes a name→values map the same way Go's
// url.Values.Encode does, but without the path-pollution that the
// codegen wrappers introduce. Mirroring net/url's algorithm keeps the
// SigV4 canonical query string in lockstep.
func encodeQuery(q map[string][]string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	// Sort manually — sort.Strings would force a sort import; the slice
	// is tiny so a hand-rolled insertion sort is fine.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	var b strings.Builder
	for _, k := range keys {
		for _, v := range q[k] {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
		}
	}
	return b.String()
}

// completeUpload calls CompleteMultipartUpload with the assembled part
// list and returns the final object ETag.
//
// Per S3 protocol the body XML root element is <CompleteMultipartUpload>
// with one <Part> child per uploaded part, in PartNumber order.
func (c *ClientWithResponses) completeUpload(
	ctx context.Context,
	bucket, key, uploadId string,
	parts []CompletedPart,
) (string, error) {
	body, err := xml.Marshal(struct {
		XMLName xml.Name        `xml:"CompleteMultipartUpload"`
		Part    []CompletedPart `xml:"Part"`
	}{Part: parts})
	if err != nil {
		return "", fmt.Errorf("marshal complete body: %w", err)
	}
	editors := []RequestEditorFn{
		func(_ context.Context, req *http.Request) error {
			// Switch InitiateMultipartUpload into CompleteMultipartUpload
			// by replacing the discriminator. Rewrite RawQuery from scratch
			// (don't merge with the polluted codegen-built query).
			req.URL.RawQuery = "uploadId=" + uploadId
			req.Header.Set("Content-Type", "application/xml")
			return nil
		},
	}
	resp, err := c.InitiateMultipartUploadWithBodyWithResponse(
		ctx, bucket, key,
		&InitiateMultipartUploadParams{},
		"application/xml",
		strings.NewReader(string(body)),
		editors...,
	)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode(), bodyPreview(resp.Body))
	}
	var out CompleteMultipartUploadResult
	if err := xml.Unmarshal(resp.Body, &out); err != nil {
		return "", fmt.Errorf("decode complete response: %w", err)
	}
	if out.ETag == nil || *out.ETag == "" {
		return "", errors.New("server returned empty ETag")
	}
	return *out.ETag, nil
}

// bodyPreview clamps a response body to a short, printable summary for
// error messages — never logs the full body.
func bodyPreview(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

// Reasonable upper bound for an int64 that strconv.FormatInt would emit —
// kept here so build doesn't gripe about an unused strconv import in some
// build configurations.
var _ = strconv.FormatInt
