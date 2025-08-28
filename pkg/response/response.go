package response

import (
	"encoding/json"
	"net/http"
)

// JSON writes the provided value as JSON with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes a standardized error response body.
func Error(w http.ResponseWriter, status int, err error) {
	if err == nil {
		w.WriteHeader(status)
		return
	}
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    http.StatusText(status),
			"message": err.Error(),
		},
	})
}
