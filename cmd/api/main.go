package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/kuetix/api"
	"github.com/kuetix/api/modules"
	"github.com/kuetix/engine"
	"github.com/kuetix/engine/engine/domain"
)

var Version string
var BuildTime string

func main() {
	// Parse command-line flags
	version := flag.Bool("version", false, "Show version")
	port := flag.String("port", "8080", "API port")
	modulesPathFlag := flag.String("modules", "modules", "Path to modules directory")
	workflowsPathFlag := flag.String("workflows", "workflows", "Path to workflows directory")
	dbPathFlag := flag.String("db", "./data", "Path to database directory")
	flag.Parse()

	// Clean paths
	modulesPath := filepath.Clean(*modulesPathFlag)
	workflowsPath := filepath.Clean(*workflowsPathFlag)
	dbPath := filepath.Clean(*dbPathFlag)

	modules.Enable()

	if Version == "" {
		Version = "dev"
	}

	if BuildTime == "" {
		BuildTime = time.Now().Format(time.RFC3339)
	}

	if *version {
		fmt.Printf("Version: %s\nBuild Time: %s\n", Version, BuildTime)
		return
	}

	// Run the API server startup workflow using engine.RunWorkflow
	options := &domain.Options{
		EngineName:    "kuetix-api",
		ConfigName:    "engine",
		Verbose:       true,
		Quiet:         false,
		Amount:        1,
		Retry:         1,
		RetryDelay:    0,
		RestartPolicy: "",
		Workflow:      "@api_server/startup",
		Version:       Version,
		BuildTime:     BuildTime,
		LogPath:       "stdout",
		Config:        &domain.Config{},
		Args: []string{
			fmt.Sprintf("port=%s", *port),
			fmt.Sprintf("modulesPath=%s", modulesPath),
			fmt.Sprintf("workflowsPath=%s", workflowsPath),
			fmt.Sprintf("dbPath=%s", dbPath),
			fmt.Sprintf("version=%s", Version),
			fmt.Sprintf("buildTime=%s", BuildTime),
		},
		Context: map[string]interface{}{
			"dbPath": dbPath,
		},
		EmbedFS:         &api.WorkflowsFS,
		EmbedFSRootPath: api.WorkflowsFSPath,
	}
	response := engine.RunWorkflow("production", options)

	// Check workflow execution result
	if response == nil {
		log.Fatal("Failed to execute API server startup workflow: nil response")
	}

	startResult, ok := response["api_server/startup"]
	if !ok {
		startResult, ok = response["@api_server/startup"]
		if !ok {
			startResult, ok = response["startup"]
		}
	}
	if ok && startResult != nil && !(*startResult).IsSuccess() {
		log.Fatalf("Failed to start API server: %v", startResult.Error)
	}

	// If we reach here, the workflow completed successfully
	// Note: The workflow StartServer action blocks, so this line won't be reached
	// unless the server stops or fails
	log.Println("API server workflow completed")
}
