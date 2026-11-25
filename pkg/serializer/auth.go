package serializer

import "encoding/json"

// RequestRawSign is the HTTP request to be signed
type RequestRawSign struct {
	Path   string
	Header string
	Body   string
}

// NewRequestSignString returns the JSON string to be signed
func NewRequestSignString(path, header, body string) string {
	req := RequestRawSign{
		Path:   path,
		Header: header,
		Body:   body,
	}
	res, _ := json.Marshal(req)
	return string(res)
}
