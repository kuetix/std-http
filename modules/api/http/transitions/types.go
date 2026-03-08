package transitions

// RouteConfigGroup defines a group of related routes (e.g., all marketplace endpoints)
type RouteConfigGroup struct {
	Path   string
	Routes []RouteConfig
}

// RouteConfig Route configuration structure
type RouteConfig struct {
	Path         string
	Method       string
	WorkflowPath string
	Description  string
}

// WorkflowRequest represents a workflow execution request
type WorkflowRequest struct {
	Workflow string                 `json:"workflow"`
	Args     map[string]interface{} `json:"args"`
}

// StandardResponse represents a generic marketplace API response
type StandardResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Errors  []string    `json:"errors,omitempty"`
	Message string      `json:"message,omitempty"`
}

type FileResponse struct {
	ContentType  string `json:"contentType"`
	Path         string `json:"path"`
	CacheControl string `json:"cacheControl"`
	Error        string `json:"error,omitempty"`
}
