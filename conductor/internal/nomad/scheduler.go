package nomad

import (
	"fmt"
	"os"
	"strings"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"

	"vela/conductor/internal/config"
	"vela/conductor/internal/domain"
	"vela/conductor/internal/secret"
)

type Scheduler interface {
	Submit(build domain.Build, product domain.Product) (jobID string, log string, err error)
	Inspect(jobID string) (JobStatus, error)
	Stop(jobID string) error
}

type JobStatus struct {
	State       string
	Description string
}

const (
	JobStateUnknown  = "unknown"
	JobStatePending  = "pending"
	JobStateRunning  = "running"
	JobStateComplete = "complete"
	JobStateFailed   = "failed"
)

type NomadScheduler struct {
	cfg    config.Config
	client *nomadapi.Client
}

func NewScheduler(cfg config.Config) (Scheduler, error) {
	if cfg.DryRun {
		return DryRunScheduler{cfg: cfg}, nil
	}

	nomadConfig := nomadapi.DefaultConfig()
	nomadConfig.Address = cfg.NomadAddr
	nomadConfig.Region = cfg.NomadRegion
	nomadConfig.Namespace = cfg.NomadNamespace
	nomadConfig.SecretID = cfg.NomadToken
	client, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		return nil, err
	}
	return NomadScheduler{cfg: cfg, client: client}, nil
}

func (s NomadScheduler) Submit(build domain.Build, product domain.Product) (string, string, error) {
	job, err := s.buildJobWithSecrets(build, product)
	if err != nil {
		return "", "", err
	}
	_, _, err = s.client.Jobs().Register(job, nil)
	if err != nil {
		return "", "", err
	}
	return *job.ID, fmt.Sprintf("submitted Nomad job %s", *job.ID), nil
}

func (s NomadScheduler) buildJob(build domain.Build, product domain.Product) *nomadapi.Job {
	job, err := s.buildJobWithSecrets(build, product)
	if err != nil {
		return nil
	}
	return job
}

func (s NomadScheduler) buildJobWithSecrets(build domain.Build, product domain.Product) (*nomadapi.Job, error) {
	jobID := jobID(build)
	taskName := "package"
	groupName := "builder"
	driver := "docker"

	config := map[string]any{
		"image":   s.cfg.RunnerImage,
		"command": "sh",
		"args": []string{
			"-lc",
			s.cfg.RunnerCommand,
		},
	}
	if s.cfg.RunnerDockerPrivileged {
		config["privileged"] = true
	}
	volumes := []string{}
	if s.cfg.DaggerDockerSocket != "" {
		volumes = append(volumes, fmt.Sprintf("%s:/var/run/docker.sock", s.cfg.DaggerDockerSocket))
	}
	if s.cfg.RunnerBunCacheDir != "" {
		volumes = append(volumes, fmt.Sprintf("%s:/root/.bun/install/cache", s.cfg.RunnerBunCacheDir))
	}
	if s.cfg.RunnerDaggerCacheDir != "" {
		volumes = append(volumes, fmt.Sprintf("%s:/root/.cache/dagger", s.cfg.RunnerDaggerCacheDir))
	}
	if s.cfg.ArtifactRoot != "" {
		volumes = append(volumes, fmt.Sprintf("%s:/artifacts", s.cfg.ArtifactRoot))
	}
	if len(volumes) > 0 {
		config["volumes"] = volumes
	}
	constraintClass := s.cfg.LinuxNodeClass
	if build.Target == "windows" {
		driver = "raw_exec"
		constraintClass = s.cfg.WindowsNodeClass
		config = map[string]any{
			"command": "powershell.exe",
			"args": []string{
				"-NoProfile",
				"-ExecutionPolicy",
				"Bypass",
				"-Command",
				s.cfg.WindowsRunnerCommand,
			},
		}
	}

	env := map[string]string{
		"BUILD_ID":          build.ID,
		"PACKAGING_API_URL": s.cfg.APIBaseURL,
		"RUNNER_ID":         fmt.Sprintf("nomad-%s", build.ID),
		"PRODUCT_ID":        build.ProductID,
		"PRODUCT_NAME":      product.Name,
		"TARGET":            build.Target,
		"PROFILE":           build.Profile,
		"CUSTOMER":          build.Customer,
		"VERSION":           build.Version,
		"MINIO_BUCKET":      s.cfg.MinIOBucket,
	}
	addEnv(env, "MINIO_PUBLIC_URL", s.cfg.MinIOPublicURL)
	addEnv(env, "GITLAB_PACKAGE_BASE_URL", s.cfg.GitLabPackageBaseURL)
	addEnv(env, "GITLAB_PACKAGE_TOKEN", s.cfg.GitLabPackageToken)
	if build.Target == "linux" && s.cfg.ArtifactRoot != "" {
		addEnv(env, "ARTIFACT_ROOT", "/artifacts")
	} else {
		addEnv(env, "ARTIFACT_ROOT", s.cfg.ArtifactRoot)
	}
	addEnv(env, "DAGGER_RUNNER_HOST", s.cfg.DaggerRunnerHost)
	addEnv(env, "DAGGER_CACHE_DIR", "/root/.cache/dagger")
	addEnv(env, "BUN_INSTALL_CACHE_DIR", "/root/.bun/install/cache")
	addEnv(env, "HTTP_PROXY", envValue("HTTP_PROXY"))
	addEnv(env, "HTTPS_PROXY", envValue("HTTPS_PROXY"))
	addEnv(env, "NO_PROXY", envValue("NO_PROXY"))
	addEnv(env, "http_proxy", envValue("http_proxy"))
	addEnv(env, "https_proxy", envValue("https_proxy"))
	addEnv(env, "no_proxy", envValue("no_proxy"))
	addEnv(env, "LSAPCTL_BUILD_CMD", s.cfg.LSAPCTLBuildCmd)
	addEnv(env, "LINUX_PACKAGER_CMD", s.cfg.LinuxPackagerCmd)
	addEnv(env, "WINDOWS_PACKAGER_CMD", s.cfg.WindowsPackagerCmd)
	addEnv(env, "WINDOWS_SIGNING_CMD", s.cfg.WindowsSigningCmd)
	addEnv(env, "SECRET_ENV_PREFIX", s.cfg.SecretEnvPrefix)
	for key, value := range build.Variables {
		env["VAR_"+strings.ToUpper(key)] = fmt.Sprint(value)
	}
	resolver, err := secret.NewResolver(s.cfg)
	if err != nil {
		return nil, err
	}
	secretEnv, err := resolver.ResolveBuild(build)
	if err != nil {
		return nil, err
	}
	for key, value := range secretEnv {
		env[key] = value
	}

	return &nomadapi.Job{
		ID:          &jobID,
		Name:        &jobID,
		Type:        ptr("batch"),
		Datacenters: []string{"dc1"},
		TaskGroups: []*nomadapi.TaskGroup{{
			Name:             &groupName,
			Count:            ptr(1),
			Constraints:      placementConstraints(build.Target, constraintClass),
			RestartPolicy:    restartPolicy(s.cfg),
			ReschedulePolicy: reschedulePolicy(s.cfg),
			EphemeralDisk:    ephemeralDisk(s.cfg),
			MaxRunDuration:   durationPtr(s.cfg.RunnerMaxRunDuration),
			Tasks: []*nomadapi.Task{{
				Name:        taskName,
				Driver:      driver,
				Config:      config,
				Env:         env,
				Resources:   taskResources(s.cfg),
				LogConfig:   logConfig(s.cfg),
				KillTimeout: durationPtr(s.cfg.RunnerKillTimeout),
			}},
		}},
	}, nil
}

func (s NomadScheduler) Inspect(jobID string) (JobStatus, error) {
	allocations, _, err := s.client.Jobs().Allocations(jobID, true, nil)
	if err != nil {
		return JobStatus{}, err
	}
	if len(allocations) == 0 {
		return JobStatus{State: JobStatePending, Description: "nomad job has no allocations yet"}, nil
	}

	complete := 0
	for _, allocation := range allocations {
		description := allocation.ClientDescription
		if allocation.DesiredDescription != "" {
			description = allocation.DesiredDescription
		}
		if allocation.ClientStatus == "failed" || allocation.ClientStatus == "lost" {
			return JobStatus{State: JobStateFailed, Description: description}, nil
		}
		for taskName, task := range allocation.TaskStates {
			if task.Failed {
				return JobStatus{State: JobStateFailed, Description: fmt.Sprintf("task %s failed", taskName)}, nil
			}
		}
		switch allocation.ClientStatus {
		case "running":
			return JobStatus{State: JobStateRunning, Description: description}, nil
		case "pending":
			return JobStatus{State: JobStatePending, Description: description}, nil
		case "complete":
			complete++
		}
	}
	if complete == len(allocations) {
		return JobStatus{State: JobStateComplete, Description: "all allocations completed"}, nil
	}
	return JobStatus{State: JobStateUnknown, Description: "nomad allocation state is mixed"}, nil
}

func (s NomadScheduler) Stop(jobID string) error {
	_, _, err := s.client.Jobs().Deregister(jobID, false, nil)
	return err
}

type DryRunScheduler struct {
	cfg config.Config
}

func (s DryRunScheduler) Submit(build domain.Build, product domain.Product) (string, string, error) {
	id := jobID(build)
	log := fmt.Sprintf(
		"dry-run: would submit Nomad batch job %s for product=%s target=%s profile=%s version=%s bucket=%s",
		id,
		product.ID,
		build.Target,
		build.Profile,
		build.Version,
		s.cfg.MinIOBucket,
	)
	return id, log, nil
}

func (s DryRunScheduler) Inspect(jobID string) (JobStatus, error) {
	return JobStatus{State: JobStateUnknown, Description: "dry-run scheduler does not inspect nomad allocations"}, nil
}

func (s DryRunScheduler) Stop(jobID string) error {
	return nil
}

func jobID(build domain.Build) string {
	return fmt.Sprintf("pkg-%s-%s", build.ProductID, build.ID)
}

func ptr[T any](value T) *T {
	return &value
}

func addEnv(env map[string]string, key string, value string) {
	if value != "" {
		env[key] = value
	}
}

func envValue(key string) string {
	return os.Getenv(key)
}

func placementConstraints(target string, nodeClass string) []*nomadapi.Constraint {
	constraints := []*nomadapi.Constraint{}
	if target == "windows" {
		constraints = append(constraints, nomadapi.NewConstraint("${attr.kernel.name}", "=", "windows"))
	} else {
		constraints = append(constraints, nomadapi.NewConstraint("${attr.kernel.name}", "=", "linux"))
	}
	if nodeClass != "" {
		constraints = append(constraints, nomadapi.NewConstraint("${meta.node_class}", "=", nodeClass))
	}
	return constraints
}

func taskResources(cfg config.Config) *nomadapi.Resources {
	resources := &nomadapi.Resources{
		CPU:      ptr(positiveOrDefault(cfg.RunnerCPU, 2000)),
		MemoryMB: ptr(positiveOrDefault(cfg.RunnerMemoryMB, 4096)),
		DiskMB:   ptr(positiveOrDefault(cfg.RunnerDiskMB, 4096)),
	}
	if cfg.RunnerMemoryMaxMB > 0 {
		resources.MemoryMaxMB = ptr(cfg.RunnerMemoryMaxMB)
	}
	return resources
}

func logConfig(cfg config.Config) *nomadapi.LogConfig {
	return &nomadapi.LogConfig{
		MaxFiles:      ptr(positiveOrDefault(cfg.RunnerLogMaxFiles, 10)),
		MaxFileSizeMB: ptr(positiveOrDefault(cfg.RunnerLogMaxFileMB, 20)),
		Disabled:      ptr(false),
	}
}

func restartPolicy(cfg config.Config) *nomadapi.RestartPolicy {
	return &nomadapi.RestartPolicy{
		Attempts: ptr(nonNegativeOrDefault(cfg.RunnerRestartAttempts, 0)),
		Mode:     ptr("fail"),
	}
}

func reschedulePolicy(cfg config.Config) *nomadapi.ReschedulePolicy {
	return &nomadapi.ReschedulePolicy{
		Attempts:  ptr(nonNegativeOrDefault(cfg.RunnerRescheduleAttempts, 0)),
		Unlimited: ptr(false),
	}
}

func ephemeralDisk(cfg config.Config) *nomadapi.EphemeralDisk {
	return &nomadapi.EphemeralDisk{
		Sticky:  ptr(false),
		Migrate: ptr(false),
		SizeMB:  ptr(positiveOrDefault(cfg.RunnerDiskMB, 4096)),
	}
}

func durationPtr(value time.Duration) *time.Duration {
	if value <= 0 {
		return nil
	}
	return &value
}

func nonNegativeOrDefault(value int, fallback int) int {
	if value < 0 {
		return fallback
	}
	return value
}

func positiveOrDefault(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
