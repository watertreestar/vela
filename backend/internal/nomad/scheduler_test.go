package nomad

import (
	"strings"
	"testing"

	"packaging-platform/backend/internal/config"
	"packaging-platform/backend/internal/domain"
)

func TestNomadJobRunsPackagingRunner(t *testing.T) {
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:    "http://packaging-api:8080",
		MinIOBucket:   "packaging-artifacts",
		RunnerImage:   "packaging-runner:test",
		RunnerCommand: "bun /app/package-runner.js",
	}}

	job := scheduler.buildJob(domain.Build{
		ID:        "build-1",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "latest",
	}, domain.Product{ID: "lsap", Name: "LSAP"})

	task := job.TaskGroups[0].Tasks[0]
	if task.Config["image"] != "packaging-runner:test" {
		t.Fatalf("runner image not configured: %#v", task.Config["image"])
	}
	args := task.Config["args"].([]string)
	if !strings.Contains(args[1], "package-runner.js") {
		t.Fatalf("runner command not configured: %#v", args)
	}
	if task.Env["PACKAGING_API_URL"] != "http://packaging-api:8080" {
		t.Fatalf("api env missing: %#v", task.Env)
	}
	if task.Env["TARGET"] != "linux" {
		t.Fatalf("target env missing: %#v", task.Env)
	}
}

func TestNomadJobUsesRawExecForWindowsRunner(t *testing.T) {
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:           "http://packaging-api:8080",
		MinIOBucket:          "packaging-artifacts",
		WindowsNodeClass:     "windows-packager",
		WindowsRunnerCommand: "bun C:\\packaging-runner\\package-runner.js",
	}}

	job := scheduler.buildJob(domain.Build{
		ID:        "build-2",
		ProductID: "lsap",
		Target:    "windows",
		Profile:   "qa",
		Version:   "latest",
	}, domain.Product{ID: "lsap", Name: "LSAP"})

	group := job.TaskGroups[0]
	task := group.Tasks[0]
	if task.Driver != "raw_exec" {
		t.Fatalf("windows job should use raw_exec: %s", task.Driver)
	}
	if task.Config["command"] != "powershell.exe" {
		t.Fatalf("windows command not configured: %#v", task.Config)
	}
	if group.Constraints[0].RTarget != "windows" {
		t.Fatalf("windows kernel constraint missing: %#v", group.Constraints)
	}
	if group.Constraints[1].RTarget != "windows-packager" {
		t.Fatalf("windows node class missing: %#v", group.Constraints)
	}
}
