package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                 string
	DataDir              string
	ProductsDir          string
	StoreDriver          string
	DatabaseURL          string
	DryRun               bool
	NomadAddr            string
	NomadRegion          string
	NomadNamespace       string
	APIBaseURL           string
	MinIOEndpoint        string
	MinIOAccessKey       string
	MinIOSecretKey       string
	MinIOBucket          string
	MinIOPublicURL       string
	MinIOUseSSL          bool
	ArtifactStore        string
	RunnerImage          string
	RunnerCommand        string
	DaggerRunnerHost     string
	DaggerDockerSocket   string
	RunnerCacheDir       string
	RunnerBunCacheDir    string
	RunnerDaggerCacheDir string
	LinuxNodeClass       string
	WindowsNodeClass     string
	WindowsRunnerCommand string
	RunnerTimeout        time.Duration
	ReconcileInterval    time.Duration
	ArtifactRoot         string
	LSAPCTLBuildCmd      string
	LinuxPackagerCmd     string
	WindowsPackagerCmd   string
}

func Load() Config {
	return Config{
		Port:                 env("PORT", "8080"),
		DataDir:              env("DATA_DIR", "./data"),
		ProductsDir:          env("PRODUCTS_DIR", "../products"),
		StoreDriver:          env("STORE_DRIVER", "postgres"),
		DatabaseURL:          env("DATABASE_URL", "postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable"),
		DryRun:               env("DRY_RUN", "true") != "false",
		NomadAddr:            env("NOMAD_ADDR", "http://127.0.0.1:4646"),
		NomadRegion:          env("NOMAD_REGION", "global"),
		NomadNamespace:       env("NOMAD_NAMESPACE", "default"),
		APIBaseURL:           env("PACKAGING_API_URL", "http://127.0.0.1:8080"),
		MinIOEndpoint:        os.Getenv("MINIO_ENDPOINT"),
		MinIOAccessKey:       os.Getenv("MINIO_ACCESS_KEY"),
		MinIOSecretKey:       os.Getenv("MINIO_SECRET_KEY"),
		MinIOBucket:          env("MINIO_BUCKET", "packaging-artifacts"),
		MinIOPublicURL:       os.Getenv("MINIO_PUBLIC_URL"),
		MinIOUseSSL:          env("MINIO_USE_SSL", "false") == "true",
		ArtifactStore:        env("ARTIFACT_STORE", artifactStoreDefault()),
		RunnerImage:          env("PACKAGING_RUNNER_IMAGE", "packaging-runner:local"),
		RunnerCommand:        env("PACKAGING_RUNNER_COMMAND", "bun /app/package-runner.js"),
		DaggerRunnerHost:     os.Getenv("DAGGER_RUNNER_HOST"),
		DaggerDockerSocket:   env("DAGGER_DOCKER_SOCKET", "/var/run/docker.sock"),
		RunnerCacheDir:       env("RUNNER_CACHE_DIR", "/var/cache/packaging"),
		RunnerBunCacheDir:    env("RUNNER_BUN_CACHE_DIR", "/var/cache/packaging/bun"),
		RunnerDaggerCacheDir: env("RUNNER_DAGGER_CACHE_DIR", "/var/cache/packaging/dagger"),
		LinuxNodeClass:       env("NOMAD_LINUX_NODE_CLASS", "linux-packager"),
		WindowsNodeClass:     env("NOMAD_WINDOWS_NODE_CLASS", "windows-packager"),
		WindowsRunnerCommand: env("WINDOWS_RUNNER_COMMAND", "bun C:\\packaging-runner\\package-runner.js"),
		RunnerTimeout:        envDuration("RUNNER_TIMEOUT_SECONDS", 1800),
		ReconcileInterval:    envDuration("RECONCILE_INTERVAL_SECONDS", 30),
		ArtifactRoot:         os.Getenv("ARTIFACT_ROOT"),
		LSAPCTLBuildCmd:      os.Getenv("LSAPCTL_BUILD_CMD"),
		LinuxPackagerCmd:     os.Getenv("LINUX_PACKAGER_CMD"),
		WindowsPackagerCmd:   os.Getenv("WINDOWS_PACKAGER_CMD"),
	}
}

func artifactStoreDefault() string {
	if os.Getenv("MINIO_ENDPOINT") != "" {
		return "minio"
	}
	return "local"
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envDuration(key string, fallbackSeconds int) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return time.Duration(fallbackSeconds) * time.Second
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return time.Duration(fallbackSeconds) * time.Second
	}
	return time.Duration(seconds) * time.Second
}
