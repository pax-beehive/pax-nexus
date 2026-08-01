package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/cohorttask"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/datasetinstall"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/transport/httpapi/handler"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:58081", "HTTP listen address")
	root := flag.String(
		"root",
		"web/llmwiki-benchmark-dashboard/public/acceptance",
		"knowledge eval result root",
	)
	datasetRoot := flag.String(
		"dataset-root",
		".build/datasets/llmwiki",
		"raw and prepared benchmark dataset root",
	)
	preparedRootFlag := flag.String(
		"prepared-root",
		"",
		"prepared benchmark dataset root; defaults to <dataset-root>/prepared",
	)
	taskRoot := flag.String(
		"task-root",
		".build/knowledge-eval/tasks",
		"persistent experiment task state root",
	)
	cohortRoot := flag.String(
		"cohort-root",
		".build/knowledge-eval/cohort-campaigns",
		"persistent cohort campaign state root",
	)
	installTaskRoot := flag.String(
		"dataset-install-task-root",
		".build/knowledge-eval/dataset-install-tasks",
		"persistent dataset install task state root",
	)
	deepSeekBaseURL := flag.String(
		"deepseek-base-url",
		"https://api.deepseek.com",
		"DeepSeek API base URL for paid experiment tasks",
	)
	codeRevision := flag.String(
		"code-revision",
		"working-tree",
		"solution code revision recorded on new task artifacts",
	)
	flag.Parse()
	preparedRoot := *preparedRootFlag
	if preparedRoot == "" {
		preparedRoot = filepath.Join(*datasetRoot, "prepared")
	}

	registry, err := dashboard.NewRegistry(
		*root,
		dashboard.WithPreparedRoot(preparedRoot),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure knowledge eval registry: %v\n", err)
		os.Exit(1)
	}
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	planner, err := experimenttask.NewPlanner(registry, apiKey != "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure knowledge eval task planner: %v\n", err)
		os.Exit(1)
	}
	executor, err := experimenttask.NewSessionExecutor(experimenttask.SessionExecutorConfig{
		PreparedRoot: preparedRoot, ResultRoot: *root,
		APIKey: apiKey, BaseURL: *deepSeekBaseURL, CodeRevision: *codeRevision,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure knowledge eval task executor: %v\n", err)
		os.Exit(1)
	}
	taskManager, err := experimenttask.NewManager(experimenttask.ManagerConfig{
		Directory: *taskRoot, Previewer: planner, Executor: executor,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure knowledge eval task manager: %v\n", err)
		os.Exit(1)
	}
	defer taskManager.Close()
	cohortManager, err := cohorttask.NewManager(cohorttask.ManagerConfig{
		Directory: *cohortRoot, Catalog: registry, Tasks: taskManager,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure knowledge eval cohort manager: %v\n", err)
		os.Exit(1)
	}
	defer cohortManager.Close()
	installRunner, err := datasetinstall.NewCommandRunner(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure dataset install runner: %v\n", err)
		os.Exit(1)
	}
	installManager, err := datasetinstall.NewManager(datasetinstall.ManagerConfig{
		DataRoot: *datasetRoot, Directory: *installTaskRoot, Runner: installRunner,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure dataset install manager: %v\n", err)
		os.Exit(1)
	}
	defer installManager.Close()
	httpHandler, err := handler.New(
		registry,
		handler.WithTaskService(taskManager),
		handler.WithCohortService(cohortManager),
		handler.WithDatasetInstallService(installManager),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure knowledge eval HTTP handler: %v\n", err)
		os.Exit(1)
	}
	hertz := server.Default(server.WithHostPorts(*listen))
	hertz.Use(handler.InstanceMiddleware(httpHandler))
	register(hertz)
	hertz.Spin()
}
