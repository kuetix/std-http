package transitions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kuetix/engine"
	"github.com/kuetix/engine/engine/domain"
	"github.com/kuetix/engine/engine/domain/interfaces"
	"github.com/kuetix/engine/engine/workflow"
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
func (h *httpTransitions) WorkflowExecutor(workflowPath string, w http.ResponseWriter, r *http.Request, route map[string]interface{}) (result domain.FlowStepResult) {
	options := h.Ctx.Engine.GetApplication().Env.Options

	// Parse request into workflow arguments
	options.Args = []string{
		// Add configuration to args
		fmt.Sprintf("modulesPath=%s", h.modulesPath),
		fmt.Sprintf("workflowsPath=%s", h.workflowsPath),
		fmt.Sprintf("version=%s", h.version),
		fmt.Sprintf("buildTime=%s", h.buildTime),
	}

	urlString := map[string]interface{}{}
	queryString := map[string]interface{}{}
	headers := map[string]interface{}{}

	query := r.URL.Query()
	if require, ok := route["require"].(map[string]interface{}); ok {
		if url, ok := require["url"].([]interface{}); ok {
			for _, pattern := range url {
				urlString[pattern.(string)] = r.PathValue(pattern.(string))
			}
		}

		// Parse query parameters
		if qs, ok := require["qs"].([]interface{}); ok {
			for _, k := range qs {
				key := k.(string)
				if query.Has(key) {
					queryString[key] = query.Get(key)
				}
			}
		}

		// Extract Authorization header if present
		if hs, ok := require["headers"].([]interface{}); ok {
			for _, k := range hs {
				key := k.(string)
				headers[key] = r.Header.Get(key)
			}
		}
	}

	context := map[string]interface{}{
		"http": map[string]interface{}{
			"request":  r,
			"response": w,
			"method":   r.Method,
			"query":    query,
			"path":     r.URL.Path,
			"body":     "",
			"bodyData": nil,
		},
		"qs":      queryString,
		"headers": headers,
		"url":     urlString,
		"values":  urlString,
	}

	for key, value := range options.Context {
		context[key] = value
	}

	// Parse JSON body for POST/PUT/DELETE requests (clients may send a JSON
	// body with DELETE, e.g. pkg.kuetix.com's removePackage)
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			respondError(w, "Failed to read request body", http.StatusBadRequest)
			result.Success = false
			result.Error = err
			return
		}
		context["http"].(map[string]interface{})["body"] = body

		if len(body) > 0 {
			var jsonBody map[string]interface{}
			if err := json.Unmarshal(body, &jsonBody); err != nil {
				respondError(w, "Invalid JSON in request body", http.StatusBadRequest)
				result.Success = false
				result.Error = err
				return
			}
			context["json"] = jsonBody
		}
	}

	// Execute the workflow
	if !strings.HasPrefix(workflowPath, "@") {
		workflowPath = filepath.Join(h.workflowsPath, workflowPath)
	}
	responses := engine.RunWorkflow("production", &domain.Options{
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
		result.StatusCode = http.StatusInternalServerError
		var errorMessages []string = make([]string, 0)
		if response != nil && response.Error != nil {
			result.StatusCode = response.StatusCode
			issues := response.Error.Errors()
			for _, issue := range issues {
				s := issue.Error()
				if strings.Contains(s, " trace: ") && h.Ctx.Engine.GetApplication().Env.Config.Application.Debug != true {
					continue
				}
				errorMessages = append(errorMessages, s)
			}
			result.Error = response.Error
		}
		if len(errorMessages) == 0 {
			errorMessages = append(errorMessages, "Workflow execution failed with unknown error")
		}
		respondErrors(w, errorMessages, result.StatusCode)
		result.Success = false
		return
	}

	// A workflow signals a client-facing failure with a terminal
	//   action services/common/response.Response(value: {error: "..."}, statusCode: 4xx)
	// state - the engine-idiomatic error terminal (response.ResponseError as
	// a terminal action degrades to a generic 500 in this engine). That path
	// leaves response.Error nil and only sets StatusCode + an {error: ...}
	// body, so IsSuccess() is true and the server would otherwise answer HTTP
	// 200 for every business error. Honor a >= 400 status code the workflow
	// deliberately set: same JSON envelope, correct HTTP status.
	if sc := response.StatusCode; sc >= 400 {
		respondJson(w, StandardResponse{Success: false, Data: response.Response}, nil, sc)
		result.StatusCode = sc
		result.Success = false
		if response.Error != nil {
			result.Error = response.Error
		}
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
func (h *httpTransitions) SetupCORS(options map[string]interface{}) (r domain.FlowStepResult) {
	keys := map[string]interface{}{
		"AllowedOrigins":       true,
		"AllowedMethods":       true,
		"AllowedHeaders":       true,
		"ExposedHeaders":       true,
		"MaxAge":               true,
		"AllowCredentials":     true,
		"AllowPrivateNetwork":  true,
		"OptionsPassthrough":   true,
		"OptionsSuccessStatus": true,
		"Debug":                true,
	}
	if o, ok := options["options"]; ok {
		options = o.(map[string]interface{})
	}
	opts := cors.Options{}
	for name, value := range options {
		if _, ok := keys[name]; ok {
			fmt.Printf("[CORS] %s: %v\n", name, value)
			switch name {
			case "AllowedOrigins":
				if s, k := value.(string); k {
					opts.AllowedOrigins = strings.Split(s, ",")
				} else if v, k := value.([]string); k {
					opts.AllowedOrigins = v
				} else if v, k := value.([]interface{}); k {
					for i := range v {
						opts.AllowedOrigins = append(opts.AllowedOrigins, fmt.Sprintf("%v", v[i]))
					}
				}
			case "AllowedMethods":
				if s, k := value.(string); k {
					opts.AllowedMethods = strings.Split(s, ",")
				} else if v, k := value.([]string); k {
					opts.AllowedMethods = v
				} else if v, k := value.([]interface{}); k {
					for i := range v {
						opts.AllowedMethods = append(opts.AllowedMethods, fmt.Sprintf("%v", v[i]))
					}
				}
			case "AllowedHeaders":
				if s, k := value.(string); k {
					opts.AllowedHeaders = strings.Split(s, ",")
				} else if v, k := value.([]string); k {
					opts.AllowedHeaders = v
				} else if v, k := value.([]interface{}); k {
					for i := range v {
						opts.AllowedHeaders = append(opts.AllowedHeaders, fmt.Sprintf("%v", v[i]))
					}
				}
			case "ExposedHeaders":
				if s, k := value.(string); k {
					opts.ExposedHeaders = strings.Split(s, ",")
				} else if v, k := value.([]string); k {
					opts.ExposedHeaders = v
				} else if v, k := value.([]interface{}); k {
					for i := range v {
						opts.ExposedHeaders = append(opts.ExposedHeaders, fmt.Sprintf("%v", v[i]))
					}
				}
			case "MaxAge":
				// Convert to int
				if s, k := value.(string); k {
					vint, _ := strconv.Atoi(s)
					opts.MaxAge = int(time.Duration(vint) * time.Hour)
				} else if vint, k := value.(int); k {
					opts.MaxAge = int(time.Duration(vint) * time.Hour)
				}
			case "AllowCredentials":
				// Convert to bool
				opts.AllowCredentials = value == "true"
			case "AllowPrivateNetwork":
				// Convert to bool
				opts.AllowPrivateNetwork = value == "true"
			case "OptionsPassthrough":
				// Convert to bool
				opts.OptionsPassthrough = value == "true"
			case "OptionsSuccessStatus":
				// Convert to int
				if s, k := value.(string); k {
					vint, _ := strconv.Atoi(s)
					opts.OptionsSuccessStatus = int(time.Duration(vint) * time.Second)
				} else if vint, k := value.(int); k {
					opts.OptionsSuccessStatus = int(time.Duration(vint) * time.Second)
				}
			case "Debug":
				// Convert to bool
				if s, k := value.(string); k {
					opts.Debug = s == "true"
				} else if b, k := value.(bool); k {
					opts.Debug = b
				}
			}
		}
	}
	c := cors.New(opts)

	//c := cors.New(cors.Options{
	//	//AllowedOrigins: []string{req.URL.String()},
	//	//AllowedOrigins:   []string{"http://localhost:5173"},
	//	AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
	//	AllowedHeaders:   []string{"Origin", "Content-Type", "Authorization", "sentry-trace", "baggage", "X-Requested-With", "Accept"},
	//	ExposedHeaders:   []string{"Content-Length"},
	//	AllowCredentials: true,
	//	MaxAge:           int(12 * time.Hour),
	//})
	//
	h.SetValue("CORS", c)
	r.Success = true
	r.Response = c
	return
}

// RegisterRoutes registers HTTP routes for WSL workflows
func (h *httpTransitions) RegisterRoutes(modulesPath, workflowsPath, version, buildTime string, groups map[string]interface{}) (result domain.FlowStepResult) {
	h.modulesPath = modulesPath
	h.workflowsPath = workflowsPath
	h.version = version
	h.buildTime = buildTime

	var lastPath string
	// Define routes mapped to WSL workflows
	// Register each route with its workflow
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Failed to register route:", lastPath)
			panic(r)
		}
	}()

	for path, routes := range groups {
		lastPath = path
		http.HandleFunc(path, h.handleRequestFunc(routes, path))
	}

	routesCount, err := h.checkRequests(groups)
	if err != nil {
		result.Success = false
		result.Error = err

		return
	}

	result.Success = true
	result.Response = map[string]interface{}{
		"message":    "Routes registered successfully",
		"routeCount": routesCount,
		"allWSL":     true,
	}

	return
}

func (h *httpTransitions) checkRequests(groups map[string]interface{}) (int, error) {
	// Log registered routes
	fmt.Println("\nAPI routes registered successfully (all routes execute WSL workflows):")
	fmt.Println("\nAll endpoints:")
	routesCount := 0
	for path, routes := range groups {
		for _, route := range routes.([]interface{}) {
			workflowName := route.(map[string]interface{})["workflow"].(string)
			workflowNamePath := workflowName
			f, err := h.Ctx.Engine.GetWorkflowFilePath(workflowNamePath)
			if err != nil {
				fmt.Println("Failed to get workflow file path:", err)
				return -1, err
			}

			routesCount++
			fmt.Printf("  - %s %s → %s\n    %s\n\n", route.(map[string]interface{})["method"], path, f.OriginalName, f.FilePath)
		}
	}

	return routesCount, nil
}

// handleRequestFunc handles HTTP requests for registered routes
func (h *httpTransitions) handleRequestFunc(routes interface{}, path string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var workflowPath string
		var method string
		found := false
		var route map[string]interface{}
		for _, routeMap := range routes.([]interface{}) {
			route = routeMap.(map[string]interface{})
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
		h.WorkflowExecutor(workflowPath, w, r, route)
	}
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
