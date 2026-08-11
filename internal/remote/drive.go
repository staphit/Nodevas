package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// driveRemote is the Google Drive backend. It talks the Drive v3 REST API
// directly over net/http — no SDK, so no new dependency and no vendored client
// to keep current. The API hosts are fields so tests can point them at an
// httptest server. Bundle List remains restricted by the appProperties marker;
// folder browsing and bundle download use the Drive read scope (see oauth.go).
type driveRemote struct {
	creds    oauthCreds
	tokens   *driveTokenStore
	folderID string

	http       *http.Client
	apiBase    string // https://www.googleapis.com/drive/v3
	uploadBase string // https://www.googleapis.com/upload/drive/v3
}

const (
	driveBundleMimeType = "application/vnd.nodevas.project+zip"
	driveMarkerKey      = "nodevas"
	driveMarkerValue    = "bundle"
	driveFolderMimeType = "application/vnd.google-apps.folder"
	driveHashKey        = "nodevasHash"

	driveListPageSize        = 200
	driveListMaxPages        = 5
	driveListMaxItems        = driveListPageSize * driveListMaxPages
	driveListResponseMaxSize = 2 << 20
)

// DriveFolderRef identifies a Google Drive folder visible to the connected
// account. It intentionally contains metadata only; callers still choose a
// folder ID before configuring the bundle remote.
type DriveFolderRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newDriveRemote(creds oauthCreds, tokens *driveTokenStore, folderID string) *driveRemote {
	return &driveRemote{
		creds:      creds,
		tokens:     tokens,
		folderID:   folderID,
		http:       &http.Client{Timeout: 2 * time.Minute},
		apiBase:    "https://www.googleapis.com/drive/v3",
		uploadBase: "https://www.googleapis.com/upload/drive/v3",
	}
}

func (d *driveRemote) Kind() string { return "drive" }

// accessToken returns a currently valid access token, refreshing it if the
// stored one has expired.
func (d *driveRemote) accessToken(ctx context.Context) (string, error) {
	tok, err := d.tokens.Load()
	if err != nil {
		return "", err
	}
	if tok.fresh() {
		return tok.AccessToken, nil
	}
	return d.refresh(ctx)
}

// refresh renews the access token from the stored refresh token and persists
// the result.
func (d *driveRemote) refresh(ctx context.Context) (string, error) {
	tok, err := d.tokens.Load()
	if err != nil {
		return "", err
	}
	if tok.RefreshToken == "" {
		return "", fmt.Errorf("Google Drive is not connected")
	}
	refreshed, err := refreshAccessToken(ctx, d.http, d.creds, tok.RefreshToken)
	if err != nil {
		return "", err
	}
	if refreshed.Scope == "" {
		refreshed.Scope = tok.Scope
	}
	if err := d.tokens.Save(refreshed); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

// do issues an authenticated request, retrying once with a freshly refreshed
// token if Drive answers 401 (the cached access token expired early). body is a
// factory so the request can be rebuilt for the retry.
type driveRequestBody struct {
	size int64
	open func() (io.ReadCloser, error)
}

func (d *driveRemote) do(
	ctx context.Context, method, urlStr, contentType string, body *driveRequestBody,
) (*http.Response, error) {
	token, err := d.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	send := func(tok string) (*http.Response, error) {
		var reader io.ReadCloser
		if body != nil {
			reader, err = body.open()
			if err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, urlStr, reader)
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			return nil, err
		}
		if body != nil {
			req.ContentLength = body.size
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		return d.http.Do(req)
	}
	resp, err := send(token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		token, err = d.refresh(ctx)
		if err != nil {
			return nil, err
		}
		return send(token)
	}
	return resp, nil
}

// driveError turns a non-2xx Drive response into an error carrying Drive's own
// message.
func driveError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
		return fmt.Errorf("drive: %s (%d)", parsed.Error.Message, resp.StatusCode)
	}
	return fmt.Errorf("drive request failed: %d", resp.StatusCode)
}

func decodeDriveResponse(resp *http.Response, destination any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, driveListResponseMaxSize+1))
	if err != nil {
		return err
	}
	if len(body) > driveListResponseMaxSize {
		return fmt.Errorf("Drive response exceeds %d bytes", driveListResponseMaxSize)
	}
	return json.Unmarshal(body, destination)
}

// driveFile is the subset of a Drive file resource we read back.
type driveFile struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Size          string            `json:"size"`
	ModifiedTime  string            `json:"modifiedTime"`
	MimeType      string            `json:"mimeType"`
	AppProperties map[string]string `json:"appProperties"`
}

func (f driveFile) toRef() RemoteRef {
	ref := RemoteRef{ID: f.ID, Name: f.Name}
	if n, err := strconv.ParseInt(f.Size, 10, 64); err == nil {
		ref.Size = n
	}
	if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
		ref.Modified = t
	}
	ref.Hash = f.AppProperties[driveHashKey]
	// Read ownership off the file's own appProperties rather than trusting that
	// the query which returned it filtered on the marker. ListBundles does ask
	// Drive for the marker today, but retention deletes what this flag says, and
	// that decision should not depend on a query string in another function.
	ref.AppOwned = f.AppProperties[driveMarkerKey] == driveMarkerValue
	return ref
}

type readerAtSeeker interface {
	io.ReaderAt
	io.Seeker
}

type replayableUpload struct {
	source  io.ReaderAt
	offset  int64
	size    int64
	cleanup func()
}

func (u *replayableUpload) Close() {
	if u != nil && u.cleanup != nil {
		u.cleanup()
		u.cleanup = nil
	}
}

func (u *replayableUpload) Reader() io.Reader {
	return io.NewSectionReader(u.source, u.offset, u.size)
}

// prepareReplayableUpload reuses files and byte readers without copying. A
// one-shot reader is spooled to disk so a 401 retry can replay it without a
// whole-bundle byte slice.
func prepareReplayableUpload(
	ctx context.Context, data io.Reader, declaredSize int64,
) (*replayableUpload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source, ok := data.(readerAtSeeker); ok {
		start, startErr := source.Seek(0, io.SeekCurrent)
		end, endErr := source.Seek(0, io.SeekEnd)
		_, restoreErr := source.Seek(start, io.SeekStart)
		if startErr == nil && endErr == nil && restoreErr == nil && end >= start {
			size := end - start
			if declaredSize >= 0 {
				if declaredSize > size {
					return nil, fmt.Errorf("upload size %d exceeds available data %d", declaredSize, size)
				}
				size = declaredSize
			}
			return &replayableUpload{source: source, offset: start, size: size}, nil
		}
	}

	temp, err := os.CreateTemp("", "nodevas-drive-*.veproj")
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		name := temp.Name()
		_ = temp.Close()
		_ = os.Remove(name)
	}
	var source io.Reader = data
	if declaredSize >= 0 {
		source = io.LimitReader(data, declaredSize)
	}
	written, err := copyWithContext(ctx, temp, source)
	if err != nil {
		cleanup()
		return nil, err
	}
	if declaredSize >= 0 && written != declaredSize {
		cleanup()
		return nil, fmt.Errorf("upload size %d does not match available data %d", declaredSize, written)
	}
	return &replayableUpload{source: temp, size: written, cleanup: cleanup}, nil
}

func driveMultipartParts(metadata []byte, boundary string) (prefix, suffix []byte) {
	var head bytes.Buffer
	fmt.Fprintf(&head, "--%s\r\n", boundary)
	head.WriteString("Content-Type: application/json; charset=UTF-8\r\n\r\n")
	head.Write(metadata)
	fmt.Fprintf(&head, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&head, "Content-Type: %s\r\n\r\n", driveBundleMimeType)
	return head.Bytes(), []byte("\r\n--" + boundary + "--\r\n")
}

func (d *driveRemote) Push(
	ctx context.Context, name string, data io.Reader, size int64,
) (RemoteRef, error) {
	upload, err := prepareReplayableUpload(ctx, data, size)
	if err != nil {
		return RemoteRef{}, err
	}
	defer upload.Close()
	section := io.NewSectionReader(upload.source, upload.offset, upload.size)
	hash, err := readerSHA256(ctx, section, upload.size)
	if err != nil {
		return RemoteRef{}, err
	}
	metadata := map[string]any{
		"name":     sanitizeBundleName(name) + RemoteBundleExt,
		"mimeType": driveBundleMimeType,
		"appProperties": map[string]string{
			driveMarkerKey: driveMarkerValue,
			driveHashKey:   hash,
		},
	}
	if d.folderID != "" {
		metadata["parents"] = []string{d.folderID}
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return RemoteRef{}, err
	}

	suffixToken, err := randomSuffix()
	if err != nil {
		return RemoteRef{}, err
	}
	boundary := "nodevas-" + suffixToken
	prefix, suffix := driveMultipartParts(metaJSON, boundary)
	contentLength := int64(len(prefix)) + upload.size + int64(len(suffix))
	if contentLength < upload.size {
		return RemoteRef{}, fmt.Errorf("Drive upload is too large")
	}

	q := url.Values{
		"uploadType": {"multipart"},
		"fields":     {"id,name,size,modifiedTime,appProperties"},
	}
	urlStr := d.uploadBase + "/files?" + q.Encode()
	contentType := "multipart/related; boundary=" + boundary
	body := &driveRequestBody{
		size: contentLength,
		open: func() (io.ReadCloser, error) {
			media := &contextReader{ctx: ctx, r: upload.Reader()}
			reader := io.MultiReader(bytes.NewReader(prefix), media, bytes.NewReader(suffix))
			return io.NopCloser(reader), nil
		},
	}
	resp, err := d.do(ctx, http.MethodPost, urlStr, contentType, body)
	if err != nil {
		return RemoteRef{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return RemoteRef{}, driveError(resp)
	}
	var file driveFile
	if err := decodeDriveResponse(resp, &file); err != nil {
		return RemoteRef{}, err
	}
	ref := file.toRef()
	// We just wrote the marker in the metadata above, so the object is ours even
	// if Drive's response echoed no appProperties back.
	ref.AppOwned = true
	if ref.Modified.IsZero() {
		ref.Modified = time.Now()
	}
	if ref.Size == 0 {
		ref.Size = upload.size
	}
	return ref, nil
}

func (d *driveRemote) List(ctx context.Context) ([]RemoteRef, error) {
	return d.ListBundles(ctx, d.folderID)
}

// escapeDriveQueryLiteral escapes a value embedded in a Drive query string.
// Drive uses backslash escaping inside single-quoted literals; URL encoding is
// handled separately by url.Values below.
func escapeDriveQueryLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

// ListFolders returns the folders directly under parentID. Empty and "root"
// both mean the user's Drive root, matching the config representation used by
// the backup backend.
func (d *driveRemote) ListFolders(ctx context.Context, parentID string) ([]DriveFolderRef, error) {
	parentID = parentIDOrRoot(strings.TrimSpace(parentID))
	query := fmt.Sprintf(
		"mimeType = '%s' and trashed = false and '%s' in parents",
		driveFolderMimeType, escapeDriveQueryLiteral(parentID))

	folders := make([]DriveFolderRef, 0, driveListPageSize)
	pageToken := ""
	seenTokens := map[string]struct{}{}
	for page := 0; page < driveListMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q := url.Values{
			"q":        {query},
			"fields":   {"nextPageToken,files(id,name)"},
			"orderBy":  {"name"},
			"pageSize": {strconv.Itoa(driveListPageSize)},
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		urlStr := d.apiBase + "/files?" + q.Encode()
		resp, err := d.do(ctx, http.MethodGet, urlStr, "", nil)
		if err != nil {
			return nil, err
		}
		var payload struct {
			NextPageToken string           `json:"nextPageToken"`
			Files         []DriveFolderRef `json:"files"`
		}
		if resp.StatusCode/100 != 2 {
			err := driveError(resp)
			resp.Body.Close()
			return nil, err
		}
		err = decodeDriveResponse(resp, &payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		remaining := driveListMaxItems - len(folders)
		if len(payload.Files) > remaining {
			payload.Files = payload.Files[:remaining]
		}
		folders = append(folders, payload.Files...)
		if payload.NextPageToken == "" || len(folders) >= driveListMaxItems {
			break
		}
		if _, repeated := seenTokens[payload.NextPageToken]; repeated {
			break
		}
		seenTokens[payload.NextPageToken] = struct{}{}
		pageToken = payload.NextPageToken
	}
	return folders, nil
}

// ListBundles returns Nodevas-created project bundles directly under a Drive
// folder. It intentionally does not search recursively: selecting a folder is
// an explicit user action and keeps a bundle from an unrelated child folder
// out of the import list.
func (d *driveRemote) ListBundles(ctx context.Context, parentID string) ([]RemoteRef, error) {
	parent := parentIDOrRoot(strings.TrimSpace(parentID))
	query := fmt.Sprintf(
		"appProperties has { key='%s' and value='%s' } and trashed=false and '%s' in parents",
		driveMarkerKey, driveMarkerValue, escapeDriveQueryLiteral(parent))
	q := url.Values{
		"q":        {query},
		"fields":   {"nextPageToken,files(id,name,size,modifiedTime,mimeType,appProperties)"},
		"orderBy":  {"modifiedTime desc"},
		"pageSize": {strconv.Itoa(driveListPageSize)},
	}
	refs := make([]RemoteRef, 0, driveListPageSize)
	pageToken := ""
	seenTokens := map[string]struct{}{}
	for page := 0; page < driveListMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		resp, err := d.do(ctx, http.MethodGet, d.apiBase+"/files?"+q.Encode(), "", nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			err := driveError(resp)
			resp.Body.Close()
			return nil, err
		}
		var payload struct {
			NextPageToken string      `json:"nextPageToken"`
			Files         []driveFile `json:"files"`
		}
		err = decodeDriveResponse(resp, &payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, file := range payload.Files {
			if len(refs) >= driveListMaxItems {
				break
			}
			refs = append(refs, file.toRef())
		}
		if payload.NextPageToken == "" || len(refs) >= driveListMaxItems {
			break
		}
		if _, repeated := seenTokens[payload.NextPageToken]; repeated {
			break
		}
		seenTokens[payload.NextPageToken] = struct{}{}
		pageToken = payload.NextPageToken
	}
	return refs, nil
}

func parentIDOrRoot(parentID string) string {
	if parentID == "" {
		return "root"
	}
	return parentID
}

// Delete removes one bundle from Drive. This is the only call in the app that
// destroys user data in the cloud, and what bounds its blast radius is the
// OAuth scope: uploads and deletes go out under `drive.file`, which grants
// access solely to files this application itself created (see oauth.go). Drive
// answers 404 for anything else, so even a bug that fed this function an
// arbitrary file id could not touch the user's own documents. That guarantee is
// the reason deletion is safe to run unattended at all; the marker check in
// pruneBundles narrows it further, to bundles rather than merely to our files.
//
// A file that is already gone counts as deleted, so a prune that partially
// failed can be retried without reporting work that is already done as an error.
func (d *driveRemote) Delete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("bundle id is required")
	}
	urlStr := d.apiBase + "/files/" + url.PathEscape(id)
	resp, err := d.do(ctx, http.MethodDelete, urlStr, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return driveError(resp)
	}
	return nil
}

func (d *driveRemote) Pull(ctx context.Context, id string) (io.ReadCloser, error) {
	if id == "" {
		return nil, fmt.Errorf("bundle id is required")
	}
	urlStr := d.apiBase + "/files/" + url.PathEscape(id) + "?alt=media"
	resp, err := d.do(ctx, http.MethodGet, urlStr, "", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, driveError(resp)
	}
	return resp.Body, nil
}
