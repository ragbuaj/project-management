package httpx

import (
	"encoding/json"
	"net/http"
)

// WriteJSON sends body as the response, and is the counterpart of WriteError.
//
// The two exist as a pair so that a handler never reaches for json.Encoder
// directly: doing so is how a response ends up without a Content-Type, or with
// a status written after the body has already gone out.
//
// An encoding failure cannot be reported. The status and headers are already
// on the wire by then, so turning it into a 500 is not available; the request
// log records what was written either way.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}
