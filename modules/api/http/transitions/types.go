package transitions

import (
	"github.com/kuetix/engine/pkg/workflow"
)

type httpTransitions struct {
	workflow.BaseServiceTransition
	modulesPath   string
	workflowsPath string
	version       string
	buildTime     string
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
	Message string      `json:"message,omitempty"`
}

type FileResponse struct {
	ContentType  string `json:"contentType"`
	Path         string `json:"path"`
	CacheControl string `json:"cacheControl"`
	Error        string `json:"error,omitempty"`
}
