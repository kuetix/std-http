package transitions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/kuetix/engine"
	"github.com/kuetix/engine/boot"
	"github.com/kuetix/engine/pkg/domain"
	"github.com/kuetix/engine/pkg/domain/interfaces"
	"github.com/kuetix/engine/pkg/workflow"
	"github.com/rs/cors"
)

type httpTransitions struct {
	workflow.BaseServiceTransition
	modulesPath   string
	workflowsPath string
	version       string
	buildTime     string
}

func NewHTTPTransitions() interfaces.ServiceTransitions {
	return &httpTransitions{}
}

// WorkflowExecutor executes a WSL workflow for an HTTP request
func (h *httpTransitions) WorkflowExecutor(workflowPath string, w http.ResponseWriter, r *http.Request) (result domain.FlowStepResult) {
	options := h.Ctx.Engine.GetApplication().Env.Options

	// Parse request into workflow arguments
	options.Args = []string{
		// Add configuration to args
		fmt.Sprintf("modulesPath=%s", h.modulesPath),
		fmt.Sprintf("workflowsPath=%s", h.workflowsPath),
		fmt.Sprintf("version=%s", h.version),
		fmt.Sprintf("buildTime=%s", h.buildTime),
	}

	// Parse query parameters
	query := r.URL.Query()
	for key, values := range query {
		if len(values) > 0 {
			options.Args = append(options.Args, fmt.Sprintf("%s=%s", key, values[0]))
		}
	}

	// Extract Authorization header if present
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		options.Args = append(options.Args, fmt.Sprintf("authorization=%s", authHeader))
	}

	context := map[string]interface{}{
		"http": map[string]interface{}{
			"request":  r,
			"response": w,
			"method":   r.Method,
			"headers":  r.Header,
			"query":    query,
			"path":     r.URL.Path,
			"auth":     authHeader,
			"body":     "",
			"bodyData": nil,
		},
	}
	// Parse JSON body for POST/PUT requests
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			respondError(w, "Failed to read request body", http.StatusBadRequest)
			result.Success = false
			result.Error = err
			return
		}
		context["http"].(map[string]interface{})["body"] = body

		if len(body) > 0 {
			var bodyData map[string]interface{}
			if err := json.Unmarshal(body, &bodyData); err != nil {
				respondError(w, "Invalid JSON in request body", http.StatusBadRequest)
				result.Success = false
				result.Error = err
				return
			}
			context["http"].(map[string]interface{})["bodyData"] = bodyData

			// Merge body data into args
			for key, value := range bodyData {
				options.Args = append(options.Args, fmt.Sprintf("%s=%s", key, value))
			}
		}
	}

	// Execute the workflow
	workflowPath = filepath.Join(h.workflowsPath, workflowPath)
	responses := engine.RunWorkflow(&boot.Options{
		EngineName:    "kapi-api",
		ConfigName:    "http",
		Verbose:       options.Verbose,
		Quiet:         options.Quiet,
		Amount:        1,
		Retry:         1,
		RetryDelay:    0,
		RestartPolicy: options.RestartPolicy,
		Workflow:      workflowPath,
		Version:       options.Version,
		BuildTime:     options.BuildTime,
		LogPath:       options.LogPath,
		Config:        options.Config,
		Args:          options.Args,
		Settings:      options.Settings,
		Context:       context,
	})

	var response *workflow.WorkerResponse
	responseRef, ok := responses[workflowPath]
	if ok {
		response = responseRef
	}
	base := filepath.Base(workflowPath)
	responseRef, ok = responses[base]
	if ok {
		response = responseRef
	}
	if response == nil {
		ok = false
		for _, resp := range responses {
			responseRef = resp
			ok = true
			break
		}
	}
	if ok {
		response = responseRef
	}

	// Check if workflow execution was successful
	if response == nil || !response.IsSuccess() {
		errorMsg := "Workflow execution failed"
		if response != nil && response.Error != nil {
			errorMsg = response.Error.Error()
			result.Error = response.Error
		}
		respondError(w, errorMsg, http.StatusInternalServerError)
		result.Success = false
		return
	}

	// Extract response from workflow result
	if response.Response != nil {
		// Check if the response is a file
		if fileResponse, ok := response.Response.(FileResponse); ok {
			result.Success = true
			result.Response = response.Response
			respondFile(w, r, fileResponse.Path, fileResponse.ContentType, fileResponse.CacheControl)
			return
		}

		// Send the workflow response back to client
		respondSuccess(w, response.Response)
	} else {
		respondSuccess(w, map[string]interface{}{
			"success": true,
			"message": "Workflow executed successfully",
		})
	}

	result.Success = true
	result.Response = response.Response
	return
}

// SetupCORS returns a CORS handler
func (h *httpTransitions) SetupCORS() (r domain.FlowStepResult) {
	c := cors.New(cors.Options{
		// AllowOrigins: []string{url},
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Authorization", "sentry-trace", "baggage", "X-Requested-With", "Accept"},
		ExposedHeaders:   []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           int(12 * time.Hour),
	})

	h.SetValue("CORS", c)
	r.Success = true
	r.Response = c
	return
}

func (h *httpTransitions) RegisterRoutes(modulesPath, workflowsPath, version, buildTime string, groups map[string]interface{}) (result domain.FlowStepResult) {
	h.modulesPath = modulesPath
	h.workflowsPath = workflowsPath
	h.version = version
	h.buildTime = buildTime

	// Define routes mapped to WSL workflows
	// Register each route with its workflow
	for path, routes := range groups {
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("Failed to register route:", path)
				panic(r)
			}
		}()
		http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			var workflowPath string
			var method string
			found := false
			for _, routeMap := range routes.([]interface{}) {
				route := routeMap.(map[string]interface{})
				if route["method"] == r.Method {
					found = true
					method = route["method"].(string)
					workflowPath = route["workflow"].(string)
					break
				}
			}
			if !found {
				fmt.Println("No matching route found for:", r.Method, path)
				respondError(w, "No matching route found", http.StatusNotFound)
				return
			}
			fmt.Println("Handle route:", method, path, "→", workflowPath)
			h.WorkflowExecutor(workflowPath, w, r)
		})
	}

	// Log registered routes
	fmt.Println("\nAPI routes registered successfully (all routes execute WSL workflows):")
	fmt.Println("\nAll endpoints:")
	routesCount := 0
	for path, routes := range groups {
		for _, route := range routes.([]interface{}) {
			routesCount++
			fmt.Printf("  - %s %s → %s\n", route.(map[string]interface{})["method"], path, route.(map[string]interface{})["workflow"])
		}
	}

	result.Success = true
	result.Response = map[string]interface{}{
		"message":    "Routes registered successfully",
		"routeCount": routesCount,
		"allWSL":     true,
	}
	return
}

// StartServer starts the HTTP server
func (h *httpTransitions) StartServer(port string) (result domain.FlowStepResult) {
	addr := fmt.Sprintf(":%s", port)
	fmt.Printf("\nStarting API server on %s\n", addr)
	fmt.Printf("Modules path: %s\n", h.modulesPath)
	fmt.Printf("Workflows path: %s\n", h.workflowsPath)
	fmt.Printf("All endpoints are now executing WSL workflows!\n\n")

	cRaw := h.GetValue("CORS")
	c := cRaw.(*cors.Cors)

	err := http.ListenAndServe(addr, c.Handler(http.DefaultServeMux))
	if err != nil {
		result.Success = false
		result.Error = fmt.Errorf("failed to start server: %w", err)
		return
	}

	result.Success = true
	return
}

// AFileResponse creates a response that serves a file with the specified content type and cache control
func (h *httpTransitions) AFileResponse(path, contentType, cacheControl string) (result domain.FlowStepResult) {
	result.Success = true
	result.Response = FileResponse{Path: path, ContentType: contentType, CacheControl: cacheControl}
	return
}
