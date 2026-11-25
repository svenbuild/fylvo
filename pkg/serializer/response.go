package serializer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/gob"
)

// Response base serializer
type Response struct {
	Code            int         `json:"code"`
	Data            interface{} `json:"data,omitempty"`
	AggregatedError interface{} `json:"aggregated_error,omitempty"`
	Msg             string      `json:"msg"`
	Error           string      `json:"error,omitempty"`
	CorrelationID   string      `json:"correlation_id,omitempty"`
}

// NewResponseWithGobData returns, with the Data field gob encoded, a Response
func NewResponseWithGobData(c context.Context, data interface{}) Response {
	var w bytes.Buffer
	encoder := gob.NewEncoder(&w)
	if err := encoder.Encode(data); err != nil {
		return ErrWithDetails(c, CodeInternalSetting, "Failed to encode response content", err)
	}

	return Response{Data: w.Bytes()}
}

// GobDecode decodes the Response body into the target pointer
func (r *Response) GobDecode(target interface{}) {
	src := r.Data.(string)
	raw := make([]byte, len(src)*len(src)/base64.StdEncoding.DecodedLen(len(src)))
	base64.StdEncoding.Decode(raw, []byte(src))
	decoder := gob.NewDecoder(bytes.NewBuffer(raw))
	decoder.Decode(target)
}
