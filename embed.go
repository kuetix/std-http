package http

import "embed"

//go:embed workflows
var WorkflowsFS embed.FS

const WorkflowsFSPath = "workflows"
