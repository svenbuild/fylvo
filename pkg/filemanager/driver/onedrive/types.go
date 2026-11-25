package onedrive

import (
	"encoding/gob"
	"net/url"
)

// RespError error returned by the API
type RespError struct {
	APIError APIError `json:"error"`
}

// APIError error content returned by the API
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// UploadSessionResponse multipart upload session
type UploadSessionResponse struct {
	DataContext        string   `json:"@odata.context"`
	ExpirationDateTime string   `json:"expirationDateTime"`
	NextExpectedRanges []string `json:"nextExpectedRanges"`
	UploadURL          string   `json:"uploadUrl"`
}

// FileInfo file metadata
type FileInfo struct {
	Name            string          `json:"name"`
	Size            int64           `json:"size"`
	Image           imageInfo       `json:"image"`
	ParentReference parentReference `json:"parentReference"`
	DownloadURL     string          `json:"@microsoft.graph.downloadUrl"`
	File            *file           `json:"file"`
	Folder          *folder         `json:"folder"`
}

type file struct {
	MimeType string `json:"mimeType"`
}

type folder struct {
	ChildCount int `json:"childCount"`
}

type imageInfo struct {
	Height int `json:"height"`
	Width  int `json:"width"`
}

type parentReference struct {
	Path string `json:"path"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

// UploadResult upload result
type UploadResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size uint64 `json:"size"`
}

// BatchRequests batch request
type BatchRequests struct {
	Requests []BatchRequest `json:"requests"`
}

// BatchRequest single request in a batch
type BatchRequest struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Body    interface{}       `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// BatchResponses batch response
type BatchResponses struct {
	Responses []BatchResponse `json:"responses"`
}

// BatchResponse single response in a batch
type BatchResponse struct {
	ID     string `json:"id"`
	Status int    `json:"status"`
}

// ThumbResponse thumbnail response
type ThumbResponse struct {
	Value []map[string]interface{} `json:"value"`
	URL   string                   `json:"url"`
}

// ListResponse child item listing response
type ListResponse struct {
	Value    []FileInfo `json:"value"`
	Context  string     `json:"@odata.context"`
	NextLink string     `json:"@odata.nextLink,omitempty"`
}

// oauthEndpoint OAuth endpoint address
type oauthEndpoint struct {
	token     url.URL
	authorize url.URL
}

// OAuthError OAuth endpoint error response
type OAuthError struct {
	ErrorType        string `json:"error"`
	ErrorDescription string `json:"error_description"`
	CorrelationID    string `json:"correlation_id"`
}

// Site SharePoint site info
type Site struct {
	Description string `json:"description"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	WebUrl      string `json:"webUrl"`
}

func init() {
	gob.Register(Credential{})
}

// Error implements the error interface
func (err RespError) Error() string {
	return err.APIError.Message
}
