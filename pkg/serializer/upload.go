package serializer

// GeneralUploadCallbackFailed is the response for a failed storage policy upload callback
type GeneralUploadCallbackFailed struct {
	Error string `json:"error"`
}
