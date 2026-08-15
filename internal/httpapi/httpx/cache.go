// How a read response tells the browser it has not changed. The client
// refetches every read endpoint after every mutation and on every websocket
// event, and almost all of those responses are byte-identical to the one it
// already holds; an ETag turns each of them into a 304 with no body.
//
// Nothing here ever grants a lifetime to something that can change. Files on
// disk are the truth and a watcher pushes changes, so a response cached for N
// seconds would show a graph that someone else — or the user's own text editor
// — has already edited. Changeable resources revalidate on every request; only
// Immutable, applied to a resource that genuinely cannot be rewritten, hands
// out a lifetime.

package httpx

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CacheControlRevalidate is what every ETagged read carries.
//
// "no-cache" does NOT mean "do not store" — that is "no-store". It means
// "you may store this, but you must revalidate with the origin before every
// reuse". That is exactly what this app needs: the client keeps the bytes and
// spends one conditional request to learn they are still current, instead of
// re-downloading them. This is the single most misread header in HTTP; if you
// came here to make responses "actually cache", they already do.
//
// "private", never "public". These responses are behind a login and are scoped
// to the caller's account and project. A shared cache — a corporate proxy, a
// CDN in front of the VM — holding one account's graph and handing it to the
// next account that asks for the same URL is a data leak, and "public" is the
// word that invites it.
const CacheControlRevalidate = "private, no-cache"

// CacheControlImmutable is only for a resource whose bytes cannot change for
// the life of its URL. A year is the conventional "forever" (RFC 8246 §2), and
// "immutable" tells the browser not even to revalidate on a manual reload.
const CacheControlImmutable = "private, max-age=31536000, immutable"

// maxETagBody caps what a request will buffer in order to hash it.
//
// The ETag is computed from the bytes, so the bytes have to exist all at once.
// This server is meant for a small cloud VM, and the cost is per in-flight
// request, not per process: 1 MiB times however many readers arrive at the
// same moment. A megabyte of JSON is already far past what any of these
// endpoints produce for a real project, so in practice the bound is never
// reached; when it is, the right answer is to stop paying for the ETag rather
// than to hold a large payload hostage, so the recorder gives up and streams
// the rest of the response straight through.
const maxETagBody = 1 << 20

// ETag wraps a read handler so its 200 response carries an entity tag and a
// repeat request carrying that tag gets a 304 instead of the body.
//
// It works by recording what the handler writes rather than by asking the
// handler for a version number: the handlers here assemble their JSON from
// several files, and there is no single revision that covers all of them. The
// bytes are the only honest summary of what was produced.
func ETag(handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		original := c.Writer
		recorder := &etagRecorder{ResponseWriter: original, status: http.StatusOK}
		c.Writer = recorder
		handler(c)
		c.Writer = original
		recorder.finish(c.Request)
	}
}

// Immutable declares that this exact response body can never change.
//
// Call it on the success path only, immediately before writing the body. It
// must not be a middleware that sets the header up front: an error response
// wearing a year-long lifetime would pin a transient 400 into the browser's
// cache for a year, and there is no way to take it back.
func Immutable(c *gin.Context) {
	header := c.Writer.Header()
	header.Set("Cache-Control", CacheControlImmutable)
	// The project a request targets can come from a header instead of the
	// query string (see ProjectHeader), so two projects can ask for this same
	// URL and get different bytes. With a lifetime attached and no Vary, the
	// browser would serve the first project's answer to the second one.
	header.Add("Vary", ProjectHeader)
}

// FileETag marks an attachment as revalidate-every-time and gives it a tag
// derived from the file's identity on disk.
//
// Deliberately weak. Everywhere else the tag is a hash of the bytes actually
// sent, which is a strong validator because it is a claim about those exact
// bytes. Here the tag is size and modification time, which is a claim that the
// file "is still the same file" — true in practice, but not something this
// code checked byte for byte. W/ says so honestly, and it costs nothing: a
// weak validator is all If-None-Match compares with anyway.
//
// The filename is stable but the user can upload a new file over it, so an
// attachment gets an ETag and never a lifetime.
func FileETag(c *gin.Context, size int64, modTimeUnixNano int64) {
	header := c.Writer.Header()
	header.Set("Cache-Control", CacheControlRevalidate)
	header.Add("Vary", ProjectHeader)
	header.Set("ETag", weakFileTag(size, modTimeUnixNano))
}

func weakFileTag(size, modTimeUnixNano int64) string {
	var builder strings.Builder
	builder.WriteString(`W/"`)
	appendBase36(&builder, size)
	builder.WriteByte('-')
	appendBase36(&builder, modTimeUnixNano)
	builder.WriteString(`"`)
	return builder.String()
}

func appendBase36(builder *strings.Builder, value int64) {
	if value < 0 {
		value = 0
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if value == 0 {
		builder.WriteByte('0')
		return
	}
	var scratch [13]byte
	index := len(scratch)
	for value > 0 {
		index--
		scratch[index] = digits[value%36]
		value /= 36
	}
	builder.Write(scratch[index:])
}

// etagRecorder buffers what a handler writes so the bytes can be hashed before
// any of them reach the socket. Once the buffer passes maxETagBody it stops
// pretending: what it holds is flushed, and everything after goes straight to
// the real writer.
type etagRecorder struct {
	gin.ResponseWriter
	body     bytes.Buffer
	status   int
	size     int
	streamed bool // gave up on the ETag; the real writer is in charge now
}

func (r *etagRecorder) WriteHeader(status int) {
	if !r.streamed {
		if status > 0 {
			r.status = status
		}
		return
	}
	r.ResponseWriter.WriteHeader(status)
}

// WriteHeaderNow is gin's "send the status line now". While buffering there is
// nothing to send yet, and letting it through would commit a status before the
// body has been hashed.
func (r *etagRecorder) WriteHeaderNow() {
	if r.streamed {
		r.ResponseWriter.WriteHeaderNow()
	}
}

func (r *etagRecorder) Write(data []byte) (int, error) {
	if r.streamed {
		written, err := r.ResponseWriter.Write(data)
		r.size += written
		return written, err
	}
	if r.body.Len()+len(data) > maxETagBody {
		if err := r.giveUp(); err != nil {
			return 0, err
		}
		return r.Write(data)
	}
	written, err := r.body.Write(data)
	r.size += written
	return written, err
}

func (r *etagRecorder) WriteString(text string) (int, error) {
	return r.Write([]byte(text))
}

func (r *etagRecorder) Written() bool { return r.size > 0 || r.streamed }

func (r *etagRecorder) Status() int { return r.status }

func (r *etagRecorder) Size() int { return r.size }

// Flush would force bytes onto the wire, which is the one thing buffering has
// to prevent. A handler that streams gets its wish, just by way of giveUp.
func (r *etagRecorder) Flush() {
	if !r.streamed {
		if r.giveUp() != nil {
			return
		}
	}
	r.ResponseWriter.Flush()
}

// giveUp abandons the ETag: the recorded status and body go to the real
// writer, and the recorder becomes a pass-through.
func (r *etagRecorder) giveUp() error {
	r.streamed = true
	r.ResponseWriter.WriteHeader(r.status)
	r.ResponseWriter.WriteHeaderNow()
	if r.body.Len() == 0 {
		return nil
	}
	_, err := r.ResponseWriter.Write(r.body.Bytes())
	r.body.Reset()
	return err
}

// finish is where the response is finally decided: 304, or the buffered body
// with its tag, or — for anything that is not a plain 200 — the bytes exactly
// as the handler wrote them.
func (r *etagRecorder) finish(request *http.Request) {
	if r.streamed {
		return
	}
	// Only a 200 gets a tag. An error body is not data: revalidating a 500
	// with If-None-Match would let a transient failure answer 304 and be
	// mistaken for the resource itself.
	if r.status != http.StatusOK {
		_ = r.giveUp()
		return
	}

	tag := strongBodyTag(r.body.Bytes())
	header := r.ResponseWriter.Header()
	header.Set("ETag", tag)
	header.Set("Cache-Control", CacheControlRevalidate)
	// Same URL, different project when the caller names it by header rather
	// than by ?project=. The tag itself is safe either way — it is computed
	// from the body that was actually produced for this request, so a stored
	// tag from another project simply fails to match and the caller gets a
	// 200 — but Vary is what says so out loud.
	header.Add("Vary", ProjectHeader)

	if matchesIfNoneMatch(request.Header.Values("If-None-Match"), tag) {
		// A 304 carries no body, and must not describe one it is not
		// sending. net/http strips these for a bodyless status, but the
		// stripping happens in the transport; do it here so the response is
		// correct before it gets there (and so a test recorder sees the truth).
		header.Del("Content-Type")
		header.Del("Content-Length")
		r.ResponseWriter.WriteHeader(http.StatusNotModified)
		r.ResponseWriter.WriteHeaderNow()
		return
	}
	_ = r.giveUp()
}

// strongBodyTag hashes the exact bytes being sent.
//
// Strong, not W/. A strong validator asserts byte-for-byte equality, and that
// is precisely what was checked — the tag is a hash of the response body. W/
// would claim something weaker and vaguer ("semantically equivalent"), which
// this code never established and no reader could verify. SHA-256 truncated to
// 16 bytes: this is a change detector, not a security boundary, but the input
// is attacker-influenceable content and 128 bits keeps a deliberate collision
// out of reach.
func strongBodyTag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`
}

// matchesIfNoneMatch reports whether the client already holds this entity.
//
// If-None-Match is a list, not a single value, so a whole-header string
// compare is wrong the moment a client sends two tags. "*" means "if the
// resource exists at all", which for a 200 is always true.
//
// The comparison is the weak one, as RFC 9110 §13.1.2 requires for
// If-None-Match: the W/ prefix is dropped from both sides before the opaque
// tags are compared. A client that stored our strong tag and echoes it back
// weakened still means the same entity.
func matchesIfNoneMatch(values []string, tag string) bool {
	want := strings.TrimPrefix(tag, "W/")
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if candidate == "*" {
				return true
			}
			if strings.TrimPrefix(candidate, "W/") == want {
				return true
			}
		}
	}
	return false
}
