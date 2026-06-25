package nomad

import (
	"strings"
	"testing"
	"time"

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

func TestNomadJobPassesGitLabPackageResolverEnvironment(t *testing.T) {
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:           "http://packaging-api:8080",
		MinIOBucket:          "packaging-artifacts",
		RunnerImage:          "packaging-runner:test",
		RunnerCommand:        "bun /app/package-runner.js",
		GitLabPackageBaseURL: "https://gitlab.example.test",
		GitLabPackageToken:   "gitlab-token",
	}}

	job := scheduler.buildJob(domain.Build{
		ID:        "build-gitlab",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
	}, domain.Product{ID: "lsap", Name: "LSAP"})

	task := job.TaskGroups[0].Tasks[0]
	if task.Env["GITLAB_PACKAGE_BASE_URL"] != "https://gitlab.example.test" {
		t.Fatalf("gitlab base url env missing: %#v", task.Env)
	}
	if task.Env["GITLAB_PACKAGE_TOKEN"] != "gitlab-token" {
		t.Fatalf("gitlab token env missing: %#v", task.Env)
	}
}

func TestNomadJobAppliesProductionPolicyDefaults(t *testing.T) {
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:    "http://packaging-api:8080",
		MinIOBucket:   "packaging-artifacts",
		RunnerImage:   "packaging-runner:test",
		RunnerCommand: "bun /app/package-runner.js",
	}}

	job := scheduler.buildJob(domain.Build{
		ID:        "build-policy-defaults",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
	}, domain.Product{ID: "lsap", Name: "LSAP"})

	group := job.TaskGroups[0]
	task := group.Tasks[0]
	if *task.Resources.CPU != 2000 || *task.Resources.MemoryMB != 4096 || *task.Resources.DiskMB != 4096 {
		t.Fatalf("unexpected default resources: %#v", task.Resources)
	}
	if *task.LogConfig.MaxFiles != 10 || *task.LogConfig.MaxFileSizeMB != 20 || *task.LogConfig.Disabled {
		t.Fatalf("unexpected log config: %#v", task.LogConfig)
	}
	if *group.RestartPolicy.Attempts != 0 || *group.RestartPolicy.Mode != "fail" {
		t.Fatalf("unexpected restart policy: %#v", group.RestartPolicy)
	}
	if *group.ReschedulePolicy.Attempts != 0 || *group.ReschedulePolicy.Unlimited {
		t.Fatalf("unexpected reschedule policy: %#v", group.ReschedulePolicy)
	}
	if *group.EphemeralDisk.SizeMB != 4096 || *group.EphemeralDisk.Sticky || *group.EphemeralDisk.Migrate {
		t.Fatalf("unexpected ephemeral disk: %#v", group.EphemeralDisk)
	}
}

func TestNomadJobAppliesConfiguredProductionPolicy(t *testing.T) {
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:               "http://packaging-api:8080",
		MinIOBucket:              "packaging-artifacts",
		RunnerImage:              "packaging-runner:test",
		RunnerCommand:            "bun /app/package-runner.js",
		RunnerCPU:                3500,
		RunnerMemoryMB:           6144,
		RunnerMemoryMaxMB:        8192,
		RunnerDiskMB:             10240,
		RunnerLogMaxFiles:        4,
		RunnerLogMaxFileMB:       50,
		RunnerKillTimeout:        45 * time.Second,
		RunnerMaxRunDuration:     2 * time.Hour,
		RunnerRestartAttempts:    1,
		RunnerRescheduleAttempts: 2,
		RunnerDockerPrivileged:   true,
	}}

	job := scheduler.buildJob(domain.Build{
		ID:        "build-policy-custom",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
	}, domain.Product{ID: "lsap", Name: "LSAP"})

	group := job.TaskGroups[0]
	task := group.Tasks[0]
	if *task.Resources.CPU != 3500 || *task.Resources.MemoryMB != 6144 || *task.Resources.MemoryMaxMB != 8192 || *task.Resources.DiskMB != 10240 {
		t.Fatalf("configured resources not applied: %#v", task.Resources)
	}
	if *task.LogConfig.MaxFiles != 4 || *task.LogConfig.MaxFileSizeMB != 50 {
		t.Fatalf("configured log config not applied: %#v", task.LogConfig)
	}
	if *task.KillTimeout != 45*time.Second || *group.MaxRunDuration != 2*time.Hour {
		t.Fatalf("configured durations not applied: kill=%v max=%v", task.KillTimeout, group.MaxRunDuration)
	}
	if *group.RestartPolicy.Attempts != 1 || *group.ReschedulePolicy.Attempts != 2 {
		t.Fatalf("configured retry policy not applied: restart=%#v reschedule=%#v", group.RestartPolicy, group.ReschedulePolicy)
	}
	if *group.EphemeralDisk.SizeMB != 10240 {
		t.Fatalf("configured ephemeral disk not applied: %#v", group.EphemeralDisk)
	}
	if task.Config["privileged"] != true {
		t.Fatalf("docker privileged flag not applied: %#v", task.Config)
	}
}

func TestNomadJobUsesRawExecForWindowsRunner(t *testing.T) {
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:           "http://packaging-api:8080",
		MinIOBucket:          "packaging-artifacts",
		WindowsNodeClass:     "windows-packager",
		WindowsRunnerCommand: "bun C:\\packaging-runner\\package-runner.js",
		WindowsPackagerCmd:   "iscc C:\\packaging\\setup.iss",
		WindowsSigningCmd:    "signtool sign /fd SHA256 C:\\packaging\\setup.exe",
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
	if task.Env["WINDOWS_PACKAGER_CMD"] != "iscc C:\\packaging\\setup.iss" {
		t.Fatalf("windows packager env missing: %#v", task.Env)
	}
	if task.Env["WINDOWS_SIGNING_CMD"] != "signtool sign /fd SHA256 C:\\packaging\\setup.exe" {
		t.Fatalf("windows signing env missing: %#v", task.Env)
	}
}

func TestNomadJobInjectsResolvedSecretEnvironment(t *testing.T) {
	t.Setenv("PACKAGING_SECRET_DB_ROOT_PASSWORD", "secret-value")
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:      "http://packaging-api:8080",
		MinIOBucket:     "packaging-artifacts",
		RunnerImage:     "packaging-runner:test",
		RunnerCommand:   "bun /app/package-runner.js",
		SecretBackend:   "env",
		SecretEnvPrefix: "PACKAGING_SECRET_",
	}}

	job, err := scheduler.buildJobWithSecrets(domain.Build{
		ID:        "build-secret",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		FinalConfig: map[string]any{
			"database": map[string]any{
				"rootPassword": "secret:db/root-password",
			},
		},
	}, domain.Product{ID: "lsap", Name: "LSAP"})
	if err != nil {
		t.Fatal(err)
	}

	task := job.TaskGroups[0].Tasks[0]
	if task.Env["PACKAGING_SECRET_DB_ROOT_PASSWORD"] != "secret-value" {
		t.Fatalf("secret env missing: %#v", task.Env)
	}
	if task.Env["VAR_ROOTPASSWORD"] == "secret-value" {
		t.Fatalf("secret value should not be copied into build variables: %#v", task.Env)
	}
}

func TestNomadJobInjectsCustomSecretPrefixForRunner(t *testing.T) {
	t.Setenv("CUSTOM_SECRET_DB_ROOT_PASSWORD", "custom-secret")
	scheduler := NomadScheduler{cfg: config.Config{
		APIBaseURL:      "http://packaging-api:8080",
		MinIOBucket:     "packaging-artifacts",
		RunnerImage:     "packaging-runner:test",
		RunnerCommand:   "bun /app/package-runner.js",
		SecretBackend:   "env",
		SecretEnvPrefix: "CUSTOM_SECRET_",
	}}

	job, err := scheduler.buildJobWithSecrets(domain.Build{
		ID:        "build-secret-prefix",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		FinalConfig: map[string]any{
			"database": map[string]any{
				"rootPassword": "secret:db/root-password",
			},
		},
	}, domain.Product{ID: "lsap", Name: "LSAP"})
	if err != nil {
		t.Fatal(err)
	}

	task := job.TaskGroups[0].Tasks[0]
	if task.Env["SECRET_ENV_PREFIX"] != "CUSTOM_SECRET_" {
		t.Fatalf("secret prefix missing: %#v", task.Env)
	}
	if task.Env["CUSTOM_SECRET_DB_ROOT_PASSWORD"] != "custom-secret" {
		t.Fatalf("custom secret env missing: %#v", task.Env)
	}
}
