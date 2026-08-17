package transitions

import (
	"encoding/json"
	"net/http"
	"path/filepath"
)

// Helper functions for responses
func respondJson(w http.ResponseWriter, sr StandardResponse, headers map[string]string, statusCode int) {
	var err error
	for k, v := range headers {
		w.Header().Set(k, v)
	}

	// A workflow may finish without any transition setting a status code
	// (it stays 0); WriteHeader panics below 100.
	if statusCode < 100 {
		statusCode = http.StatusInternalServerError
	}

	w.WriteHeader(statusCode)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(sr)
	if err != nil {
		errorMessage := err.Error()
		err = encoder.Encode(StandardResponse{
			Success: false,
			Error:   errorMessage,
		})
		if err != nil {
			http.Error(w, errorMessage, http.StatusInternalServerError)
			return
		}
		return
	}

	return
}

// Helper functions for responses
func respondFile(w http.ResponseWriter, r *http.Request, path string, contentType string, cacheControl string) {
	switch contentType {
	case "text/plain", "plain", "", "_default":
		w.Header().Set("Content-Type", "text/plain")
	case "text/html", "html":
		w.Header().Set("Content-Type", "text/html")
	case "application/json", "json":
		w.Header().Set("Content-Type", "application/json")
	case "application/yaml", "yaml":
		w.Header().Set("Content-Type", "application/yaml")
	case "binary/octet-stream", "binary":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(path))
	default:
		w.Header().Set("Content-Type", "text/plain")
	}

	switch cacheControl {
	case "_default", "":
		w.Header().Set("Cache-Control", "public, max-age=3600")
	case "no-cache":
		w.Header().Set("Cache-Control", "no-cache")
	default:
		w.Header().Set("Cache-Control", cacheControl)
	}

	http.ServeFile(w, r, path)
}

// Helper functions for responses
func respondSuccess(w http.ResponseWriter, data interface{}) {
	respondJson(w, StandardResponse{
		Success: true,
		Data:    data,
	}, nil, http.StatusOK)
}

func respondError(w http.ResponseWriter, message string, statusCode int) {
	respondJson(w, StandardResponse{
		Success: false,
		Error:   message,
	}, nil, statusCode)
}

func respondErrors(w http.ResponseWriter, messages []string, statusCode int) {
	respondJson(w, StandardResponse{
		Success: false,
		Errors:  messages,
	}, nil, statusCode)
}
