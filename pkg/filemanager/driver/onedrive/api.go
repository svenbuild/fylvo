package onedrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/chunk"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/chunk/backoff"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

const (
	// SmallFileSize max size for the single-file upload API
	SmallFileSize uint64 = 4 * 1024 * 1024
	// ChunkSize chunk size for server-relayed chunked upload
	ChunkSize uint64 = 10 * 1024 * 1024
	// ListRetry retry count for list requests
	ListRetry       = 1
	chunkRetrySleep = time.Second * 5

	notFoundError = "itemNotFound"
)

type RetryCtx struct{}

// GetSourcePath gets the absolute path of the file
func (info *FileInfo) GetSourcePath() string {
	res, err := url.PathUnescape(info.ParentReference.Path)
	if err != nil {
		return ""
	}

	return strings.TrimPrefix(
		path.Join(
			strings.TrimPrefix(res, "/drive/root:"),
			info.Name,
		),
		"/",
	)
}

func (client *client) getRequestURL(api string, opts ...Option) string {
	options := newDefaultOption()
	for _, o := range opts {
		o.apply(options)
	}

	base, _ := url.Parse(client.endpoints.endpointURL)
	if base == nil {
		return ""
	}

	if options.useDriverResource {
		base.Path = path.Join(base.Path, client.endpoints.driverResource, api)
	} else {
		base.Path = path.Join(base.Path, api)
	}

	return base.String()
}

// ListChildren lists child objects by path
func (client *client) ListChildren(ctx context.Context, path string) ([]FileInfo, error) {
	var requestURL string
	dst := strings.TrimPrefix(path, "/")
	if dst == "" {
		requestURL = client.getRequestURL("root/children")
	} else {
		requestURL = client.getRequestURL("root:/" + dst + ":/children")
	}

	// Add pagination parameter
	requestURL += "?$top=5000"

	var allFiles []FileInfo

	for {
		res, err := client.requestWithStr(ctx, "GET", requestURL, "", 200)
		if err != nil {
			retried := 0
			if v, ok := ctx.Value(RetryCtx{}).(int); ok {
				retried = v
			}
			if retried < ListRetry {
				retried++
				client.l.Debug("Failed to list path %q: %s, will retry in 5 seconds.", path, err)
				time.Sleep(time.Duration(5) * time.Second)
				return client.ListChildren(context.WithValue(ctx, RetryCtx{}, retried), path)
			}
			return nil, err
		}

		var (
			decodeErr error
			fileInfo  ListResponse
		)
		decodeErr = json.Unmarshal([]byte(res), &fileInfo)
		if decodeErr != nil {
			return nil, decodeErr
		}

		// Append current page results
		allFiles = append(allFiles, fileInfo.Value...)

		// Check if there's a next page
		if fileInfo.NextLink == "" {
			break
		}

		// Use the next link for the next iteration
		client.l.Debug("Load next page, next link: %s", fileInfo.NextLink)
		requestURL = fileInfo.NextLink
	}

	return allFiles, nil
}

// Meta gets file metadata by resource ID or file path
func (client *client) Meta(ctx context.Context, id string, path string) (*FileInfo, error) {
	var requestURL string
	if id != "" {
		requestURL = client.getRequestURL("items/" + id)
	} else {
		dst := strings.TrimPrefix(path, "/")
		requestURL = client.getRequestURL("root:/" + dst)
	}

	res, err := client.requestWithStr(ctx, "GET", requestURL+"?expand=thumbnails", "", 200)
	if err != nil {
		return nil, err
	}

	var (
		decodeErr error
		fileInfo  FileInfo
	)
	decodeErr = json.Unmarshal([]byte(res), &fileInfo)
	if decodeErr != nil {
		return nil, decodeErr
	}

	return &fileInfo, nil

}

// CreateUploadSession creates a chunked upload session
func (client *client) CreateUploadSession(ctx context.Context, dst string, opts ...Option) (string, error) {
	options := newDefaultOption()
	for _, o := range opts {
		o.apply(options)
	}

	dst = strings.TrimPrefix(dst, "/")
	requestURL := client.getRequestURL("root:/" + dst + ":/createUploadSession")
	body := map[string]map[string]interface{}{
		"item": {
			"@microsoft.graph.conflictBehavior": options.conflictBehavior,
		},
	}
	bodyBytes, _ := json.Marshal(body)

	res, err := client.requestWithStr(ctx, "POST", requestURL, string(bodyBytes), 200)
	if err != nil {
		return "", err
	}

	var (
		decodeErr     error
		uploadSession UploadSessionResponse
	)
	decodeErr = json.Unmarshal([]byte(res), &uploadSession)
	if decodeErr != nil {
		return "", decodeErr
	}

	return uploadSession.UploadURL, nil
}

// GetSiteIDByURL gets the site ID from a SharePoint site URL
func (client *client) GetSiteIDByURL(ctx context.Context, siteUrl string) (string, error) {
	siteUrlParsed, err := url.Parse(siteUrl)
	if err != nil {
		return "", err
	}

	hostName := siteUrlParsed.Hostname()
	relativePath := strings.Trim(siteUrlParsed.Path, "/")
	requestURL := client.getRequestURL(fmt.Sprintf("sites/%s:/%s", hostName, relativePath), WithDriverResource(false))
	res, reqErr := client.requestWithStr(ctx, "GET", requestURL, "", 200)
	if reqErr != nil {
		return "", reqErr
	}

	var (
		decodeErr error
		siteInfo  Site
	)
	decodeErr = json.Unmarshal([]byte(res), &siteInfo)
	if decodeErr != nil {
		return "", decodeErr
	}

	return siteInfo.ID, nil
}

// GetUploadSessionStatus queries the upload session status
func (client *client) GetUploadSessionStatus(ctx context.Context, uploadURL string) (*UploadSessionResponse, error) {
	res, err := client.requestWithStr(ctx, "GET", uploadURL, "", 200)
	if err != nil {
		return nil, err
	}

	var (
		decodeErr     error
		uploadSession UploadSessionResponse
	)
	decodeErr = json.Unmarshal([]byte(res), &uploadSession)
	if decodeErr != nil {
		return nil, decodeErr
	}

	return &uploadSession, nil
}

// UploadChunk uploads a chunk
func (client *client) UploadChunk(ctx context.Context, uploadURL string, content io.Reader, current *chunk.ChunkGroup) (*UploadSessionResponse, error) {
	res, err := client.request(
		ctx, "PUT", uploadURL, content,
		request.WithContentLength(current.Length()),
		request.WithHeader(http.Header{
			"Content-Range": {current.RangeHeader()},
		}),
		request.WithoutHeader([]string{"Authorization", "Content-Type"}),
		request.WithTimeout(0),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upload OneDrive chunk #%d: %w", current.Index(), err)
	}

	if current.IsLast() {
		return nil, nil
	}

	var (
		decodeErr error
		uploadRes UploadSessionResponse
	)
	decodeErr = json.Unmarshal([]byte(res), &uploadRes)
	if decodeErr != nil {
		return nil, decodeErr
	}

	return &uploadRes, nil
}

// Upload uploads a file
func (client *client) Upload(ctx context.Context, file *fs.UploadRequest) error {
	// decide whether to overwrite the file
	overwrite := "fail"
	if file.Mode&fs.ModeOverwrite == fs.ModeOverwrite {
		overwrite = "replace"
	}

	size := int(file.Props.Size)
	dst := file.Props.SavePath

	// small file, use the simple upload API
	if size <= int(SmallFileSize) {
		_, err := client.SimpleUpload(ctx, dst, file, int64(size), WithConflictBehavior(overwrite))
		return err
	}

	// large file, split into chunks
	// create upload session
	uploadURL, err := client.CreateUploadSession(ctx, dst, WithConflictBehavior(overwrite))
	if err != nil {
		return err
	}

	// Initial chunk groups
	chunks := chunk.NewChunkGroup(file, client.chunkSize, &backoff.ConstantBackoff{
		Max:   client.settings.ChunkRetryLimit(ctx),
		Sleep: chunkRetrySleep,
	}, client.settings.UseChunkBuffer(ctx), client.l, client.settings.TempPath(ctx))

	uploadFunc := func(current *chunk.ChunkGroup, content io.Reader) error {
		_, err := client.UploadChunk(ctx, uploadURL, content, current)
		return err
	}

	// upload chunks
	for chunks.Next() {
		if err := chunks.Process(uploadFunc); err != nil {
			if err := client.DeleteUploadSession(ctx, uploadURL); err != nil {
				client.l.Warning("Failed to delete upload session: %s", err)
			}
			return fmt.Errorf("failed to upload chunk #%d: %w", chunks.Index(), err)
		}
	}

	return nil
}

// DeleteUploadSession deletes an upload session
func (client *client) DeleteUploadSession(ctx context.Context, uploadURL string) error {
	_, err := client.requestWithStr(ctx, "DELETE", uploadURL, "", 204)
	if err != nil {
		return err
	}

	return nil
}

// SimpleUpload uploads a small file to dst
func (client *client) SimpleUpload(ctx context.Context, dst string, body io.Reader, size int64, opts ...Option) (*UploadResult, error) {
	options := newDefaultOption()
	for _, o := range opts {
		o.apply(options)
	}

	dst = strings.TrimPrefix(dst, "/")
	requestURL := client.getRequestURL("root:/" + dst + ":/content")
	requestURL += ("?@microsoft.graph.conflictBehavior=" + options.conflictBehavior)

	res, err := client.request(ctx, "PUT", requestURL, body, request.WithContentLength(int64(size)),
		request.WithTimeout(0),
	)
	if err != nil {
		return nil, err
	}

	var (
		decodeErr error
		uploadRes UploadResult
	)
	decodeErr = json.Unmarshal([]byte(res), &uploadRes)
	if decodeErr != nil {
		return nil, decodeErr
	}

	return &uploadRes, nil
}

// BatchDelete deletes the given files in parallel, returns the files that failed and the first error. This splits the files into
// groups of 20 and calls Delete in parallel
func (client *client) BatchDelete(ctx context.Context, dst []string) ([]string, error) {
	groupNum := len(dst)/20 + 1
	finalRes := make([]string, 0, len(dst))
	res := make([]string, 0, 20)
	var err error

	for i := 0; i < groupNum; i++ {
		end := 20*i + 20
		if i == groupNum-1 {
			end = len(dst)
		}

		client.l.Debug("Delete file group: %v.", dst[20*i:end])
		res, err = client.Delete(ctx, dst[20*i:end])
		finalRes = append(finalRes, res...)
	}

	return finalRes, err
}

// Delete deletes files in parallel, returns the files that failed and the first error,
// at most 20 due to API limits
func (client *client) Delete(ctx context.Context, dst []string) ([]string, error) {
	body := client.makeBatchDeleteRequestsBody(dst)
	res, err := client.requestWithStr(ctx, "POST", client.getRequestURL("$batch",
		WithDriverResource(false)), body, 200)
	if err != nil {
		return dst, err
	}

	var (
		decodeErr error
		deleteRes BatchResponses
	)
	decodeErr = json.Unmarshal([]byte(res), &deleteRes)
	if decodeErr != nil {
		return dst, decodeErr
	}

	// collect the files that failed to delete
	failed := getDeleteFailed(&deleteRes)
	if len(failed) != 0 {
		return failed, ErrDeleteFile
	}
	return failed, nil
}

func getDeleteFailed(res *BatchResponses) []string {
	var failed = make([]string, 0, len(res.Responses))
	for _, v := range res.Responses {
		if v.Status != 204 && v.Status != 404 {
			failed = append(failed, v.ID)
		}
	}
	return failed
}

// makeBatchDeleteRequestsBody builds the batch delete request body
func (client *client) makeBatchDeleteRequestsBody(files []string) string {
	req := BatchRequests{
		Requests: make([]BatchRequest, len(files)),
	}
	for i, v := range files {
		v = strings.TrimPrefix(v, "/")
		filePath, _ := url.Parse("/" + client.endpoints.driverResource + "/root:/")
		filePath.Path = path.Join(filePath.Path, v)
		req.Requests[i] = BatchRequest{
			ID:     v,
			Method: "DELETE",
			URL:    filePath.EscapedPath(),
		}
	}

	res, _ := json.Marshal(req)
	return string(res)
}

// GetThumbURL gets the thumbnail URL for the given size
func (client *client) GetThumbURL(ctx context.Context, dst string) (string, error) {
	dst = strings.TrimPrefix(dst, "/")
	requestURL := client.getRequestURL("root:/"+dst+":/thumbnails/0") + "/large"

	res, err := client.requestWithStr(ctx, "GET", requestURL, "", 200)
	if err != nil {
		return "", err
	}

	var (
		decodeErr error
		thumbRes  ThumbResponse
	)
	decodeErr = json.Unmarshal([]byte(res), &thumbRes)
	if decodeErr != nil {
		return "", decodeErr
	}

	if thumbRes.URL != "" {
		return thumbRes.URL, nil
	}

	if len(thumbRes.Value) == 1 {
		if res, ok := thumbRes.Value[0]["large"]; ok {
			return res.(map[string]interface{})["url"].(string), nil
		}
	}

	return "", ErrThumbSizeNotFound
}

func sysError(err error) *RespError {
	return &RespError{APIError: APIError{
		Code:    "system",
		Message: err.Error(),
	}}
}

func (client *client) request(ctx context.Context, method string, url string, body io.Reader, option ...request.Option) (string, error) {
	// get credential
	err := client.UpdateCredential(ctx)
	if err != nil {
		return "", sysError(err)
	}

	opts := []request.Option{
		request.WithHeader(http.Header{
			"Authorization": {"Bearer " + client.credential.String()},
			"Content-Type":  {"application/json"},
		}),
		request.WithContext(ctx),
		request.WithTPSLimit(
			fmt.Sprintf("policy_%d", client.policy.ID),
			client.policy.Settings.TPSLimit,
			client.policy.Settings.TPSLimitBurst,
		),
	}

	// send request
	res := client.httpClient.Request(
		method,
		url,
		body,
		append(opts, option...)...,
	)

	if res.Err != nil {
		return "", sysError(res.Err)
	}

	respBody, err := res.GetResponse()
	if err != nil {
		return "", sysError(err)
	}

	// parse the response
	var (
		errResp   RespError
		decodeErr error
	)
	// if there is an error
	if res.Response.StatusCode < 200 || res.Response.StatusCode >= 300 {
		decodeErr = json.Unmarshal([]byte(respBody), &errResp)
		if decodeErr != nil {
			client.l.Debug("Onedrive returns unknown response: %s", respBody)
			return "", sysError(decodeErr)
		}

		if res.Response.StatusCode == 429 {
			client.l.Warning("OneDrive request is throttled.")
			return "", backoff.NewRetryableErrorFromHeader(&errResp, res.Response.Header)
		}

		return "", &errResp
	}

	return respBody, nil
}

func (client *client) requestWithStr(ctx context.Context, method string, url string, body string, expectedCode int) (string, error) {
	// send request
	bodyReader := io.NopCloser(strings.NewReader(body))
	return client.request(ctx, method, url, bodyReader,
		request.WithContentLength(int64(len(body))),
	)
}
