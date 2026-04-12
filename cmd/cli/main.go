package main

import (
	"flag"
	"fmt"
	"os"

	stdCoreModules "github.com/kuetix/std-core/modules"
	http "github.com/kuetix/std-http"
	stdHttpModules "github.com/kuetix/std-http/modules"

	"github.com/kuetix/engine"
	"github.com/kuetix/engine/engine/domain"
	engineModule "github.com/kuetix/engine/modules"
)

var Version string
var BuildTime string

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <workflow_name> -[v|verbose]\n", os.Args[0])
		os.Exit(1)
	}

	workflow := os.Args[1]
	os.Args = os.Args[1:]
	verbose := flag.Bool("verbose", false, "Verbose mode")
	vFlag := flag.Bool("v", false, "Verbose mode")
	flag.Parse()

	engineModule.Enable()
	stdCoreModules.Enable()
	stdHttpModules.Enable()

	verboseMode := *verbose || *vFlag

	if BuildTime == "" {
		BuildTime = "unknown"
	}

	response := engine.RunWorkflow("production", &domain.Options{
		Version:         Version,
		BuildTime:       BuildTime,
		EngineName:      "http-cli",
		ConfigName:      "engine",
		Verbose:         verboseMode,
		Quiet:           false,
		Amount:          1,
		Retry:           1,
		RetryDelay:      0,
		RestartPolicy:   "stop",
		Workflow:        workflow,
		LogPath:         "stdout",
		Config:          &domain.Config{},
		Args:            flag.Args(),
		Context:         nil,
		Settings:        nil,
		EmbedFS:         &http.WorkflowsFS,
		EmbedFSRootPath: http.WorkflowsFSPath,
	})

	for _, res := range response {
		if res.Error != nil {
			fmt.Printf("Error: %s\n", res.Error)
			os.Exit(1)
		}
		if res.Response != nil {
			fmt.Printf("Result: %v\n", res.Response)
		}
	}
}
