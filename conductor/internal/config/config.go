package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                      string
	DataDir                   string
	ProductsDir               string
	StoreDriver               string
	DatabaseURL               string
	DryRun                    bool
	NomadAddr                 string
	NomadRegion               string
	NomadNamespace            string
	NomadToken                string
	APIBaseURL                string
	MinIOEndpoint             string
	MinIOAccessKey            string
	MinIOSecretKey            string
	MinIOBucket               string
	MinIOPublicURL            string
	MinIOUseSSL               bool
	ArtifactStore             string
	RunnerImage               string
	RunnerCommand             string
	DaggerRunnerHost          string
	DaggerDockerSocket        string
	RunnerCacheDir            string
	RunnerBunCacheDir         string
	RunnerDaggerCacheDir      string
	RunnerCPU                 int
	RunnerMemoryMB            int
	RunnerMemoryMaxMB         int
	RunnerDiskMB              int
	RunnerLogMaxFiles         int
	RunnerLogMaxFileMB        int
	RunnerKillTimeout         time.Duration
	RunnerMaxRunDuration      time.Duration
	RunnerRestartAttempts     int
	RunnerRescheduleAttempts  int
	RunnerDockerPrivileged    bool
	LinuxNodeClass            string
	WindowsNodeClass          string
	WindowsRunnerCommand      string
	RunnerTimeout             time.Duration
	ReconcileInterval         time.Duration
	NotificationRetention     time.Duration
	ArtifactRoot              string
	GitLabPackageBaseURL      string
	GitLabPackageToken        string
	LSAPCTLBuildCmd           string
	LinuxPackagerCmd          string
	WindowsPackagerCmd        string
	WindowsSigningCmd         string
	AuthEnabled               bool
	AuthAdminToken            string
	AuthOperatorToken         string
	AuthViewerToken           string
	AuthRunnerToken           string
	RunnerAuthTokenFile       string
	RunnerAuthRequired        bool
	RunnerCapabilityTTL       time.Duration
	RunnerExecutionHMACKey    string
	OIDCIssuer                string
	OIDCAudience              string
	OIDCJWKSURL               string
	OIDCTokenURL              string
	OIDCClientID              string
	OIDCClientSecret          string
	OIDCRoleClaim             string
	OIDCSubjectClaim          string
	OIDCAdminRole             string
	OIDCOperatorRole          string
	OIDCViewerRole            string
	AuthSessionHMACKey        string
	AuthSessionTTL            time.Duration
	AuthSessionStoreEnabled   bool
	AuditArchiveHMACKey       string
	AuditArchiveHMACKeyID     string
	AuditArchiveRetentionMode string
	AuditArchiveRetentionDays int
	AuditArchiveLegalHold     bool
	SecretBackend             string
	SecretFile                string
	SecretEnvPrefix           string
	SOPSFile                  string
	SOPSCommand               string
	SOPSSecretField           string
	NomadVariablePathPrefix   string
	NomadVariableItemKey      string
	VaultAddr                 string
	VaultToken                string
	VaultNamespace            string
	VaultKVMount              string
	VaultSecretField          string
}

func Load() Config {
	return Config{
		Port:                      env("PORT", "8080"),
		DataDir:                   env("DATA_DIR", "./data"),
		ProductsDir:               env("PRODUCTS_DIR", "../products"),
		StoreDriver:               env("STORE_DRIVER", "postgres"),
		DatabaseURL:               env("DATABASE_URL", "postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable"),
		DryRun:                    env("DRY_RUN", "true") != "false",
		NomadAddr:                 env("NOMAD_ADDR", "http://127.0.0.1:4646"),
		NomadRegion:               env("NOMAD_REGION", "global"),
		NomadNamespace:            env("NOMAD_NAMESPACE", "default"),
		NomadToken:                os.Getenv("NOMAD_TOKEN"),
		APIBaseURL:                env("PACKAGING_API_URL", "http://127.0.0.1:8080"),
		MinIOEndpoint:             os.Getenv("MINIO_ENDPOINT"),
		MinIOAccessKey:            os.Getenv("MINIO_ACCESS_KEY"),
		MinIOSecretKey:            os.Getenv("MINIO_SECRET_KEY"),
		MinIOBucket:               env("MINIO_BUCKET", "packaging-artifacts"),
		MinIOPublicURL:            os.Getenv("MINIO_PUBLIC_URL"),
		MinIOUseSSL:               env("MINIO_USE_SSL", "false") == "true",
		ArtifactStore:             env("ARTIFACT_STORE", artifactStoreDefault()),
		RunnerImage:               env("PACKAGING_RUNNER_IMAGE", "packaging-runner:local"),
		RunnerCommand:             env("PACKAGING_RUNNER_COMMAND", "bun /app/package-runner.js"),
		DaggerRunnerHost:          os.Getenv("DAGGER_RUNNER_HOST"),
		DaggerDockerSocket:        env("DAGGER_DOCKER_SOCKET", "/var/run/docker.sock"),
		RunnerCacheDir:            env("RUNNER_CACHE_DIR", "/var/cache/packaging"),
		RunnerBunCacheDir:         env("RUNNER_BUN_CACHE_DIR", "/var/cache/packaging/bun"),
		RunnerDaggerCacheDir:      env("RUNNER_DAGGER_CACHE_DIR", "/var/cache/packaging/dagger"),
		RunnerCPU:                 envInt("RUNNER_CPU", 2000),
		RunnerMemoryMB:            envInt("RUNNER_MEMORY_MB", 4096),
		RunnerMemoryMaxMB:         envInt("RUNNER_MEMORY_MAX_MB", 0),
		RunnerDiskMB:              envInt("RUNNER_DISK_MB", 4096),
		RunnerLogMaxFiles:         envInt("RUNNER_LOG_MAX_FILES", 10),
		RunnerLogMaxFileMB:        envInt("RUNNER_LOG_MAX_FILE_MB", 20),
		RunnerKillTimeout:         envDuration("RUNNER_KILL_TIMEOUT_SECONDS", 30),
		RunnerMaxRunDuration:      envDuration("RUNNER_MAX_RUN_SECONDS", 3600),
		RunnerRestartAttempts:     envInt("RUNNER_RESTART_ATTEMPTS", 0),
		RunnerRescheduleAttempts:  envInt("RUNNER_RESCHEDULE_ATTEMPTS", 0),
		RunnerDockerPrivileged:    env("RUNNER_DOCKER_PRIVILEGED", "false") == "true",
		LinuxNodeClass:            env("NOMAD_LINUX_NODE_CLASS", "linux-packager"),
		WindowsNodeClass:          env("NOMAD_WINDOWS_NODE_CLASS", "windows-packager"),
		WindowsRunnerCommand:      env("WINDOWS_RUNNER_COMMAND", "bun C:\\packaging-runner\\package-runner.js"),
		RunnerTimeout:             envDuration("RUNNER_TIMEOUT_SECONDS", 1800),
		ReconcileInterval:         envDuration("RECONCILE_INTERVAL_SECONDS", 30),
		NotificationRetention:     envDays("NOTIFICATION_RETENTION_DAYS", 90),
		ArtifactRoot:              os.Getenv("ARTIFACT_ROOT"),
		GitLabPackageBaseURL:      os.Getenv("GITLAB_PACKAGE_BASE_URL"),
		GitLabPackageToken:        os.Getenv("GITLAB_PACKAGE_TOKEN"),
		LSAPCTLBuildCmd:           os.Getenv("LSAPCTL_BUILD_CMD"),
		LinuxPackagerCmd:          os.Getenv("LINUX_PACKAGER_CMD"),
		WindowsPackagerCmd:        os.Getenv("WINDOWS_PACKAGER_CMD"),
		WindowsSigningCmd:         os.Getenv("WINDOWS_SIGNING_CMD"),
		AuthEnabled:               env("AUTH_ENABLED", "false") == "true",
		AuthAdminToken:            os.Getenv("AUTH_ADMIN_TOKEN"),
		AuthOperatorToken:         os.Getenv("AUTH_OPERATOR_TOKEN"),
		AuthViewerToken:           os.Getenv("AUTH_VIEWER_TOKEN"),
		AuthRunnerToken:           os.Getenv("AUTH_RUNNER_TOKEN"),
		RunnerAuthTokenFile:       env("PACKAGING_API_TOKEN_FILE", "vela-runner-auth"),
		RunnerAuthRequired:        env("RUNNER_AUTH_REQUIRED", "false") == "true",
		RunnerCapabilityTTL:       envDuration("RUNNER_CAPABILITY_TTL_SECONDS", 900),
		RunnerExecutionHMACKey:    os.Getenv("RUNNER_EXECUTION_HMAC_KEY"),
		OIDCIssuer:                os.Getenv("OIDC_ISSUER"),
		OIDCAudience:              os.Getenv("OIDC_AUDIENCE"),
		OIDCJWKSURL:               os.Getenv("OIDC_JWKS_URL"),
		OIDCTokenURL:              os.Getenv("OIDC_TOKEN_URL"),
		OIDCClientID:              os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:          os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRoleClaim:             env("OIDC_ROLE_CLAIM", "roles"),
		OIDCSubjectClaim:          env("OIDC_SUBJECT_CLAIM", "sub"),
		OIDCAdminRole:             env("OIDC_ADMIN_ROLE", "admin"),
		OIDCOperatorRole:          env("OIDC_OPERATOR_ROLE", "operator"),
		OIDCViewerRole:            env("OIDC_VIEWER_ROLE", "viewer"),
		AuthSessionHMACKey:        os.Getenv("AUTH_SESSION_HMAC_KEY"),
		AuthSessionTTL:            envDuration("AUTH_SESSION_TTL_SECONDS", 3600),
		AuthSessionStoreEnabled:   env("AUTH_SESSION_STORE_ENABLED", "false") == "true",
		AuditArchiveHMACKey:       os.Getenv("AUDIT_ARCHIVE_HMAC_KEY"),
		AuditArchiveHMACKeyID:     os.Getenv("AUDIT_ARCHIVE_HMAC_KEY_ID"),
		AuditArchiveRetentionMode: env("AUDIT_ARCHIVE_RETENTION_MODE", ""),
		AuditArchiveRetentionDays: envInt("AUDIT_ARCHIVE_RETENTION_DAYS", 0),
		AuditArchiveLegalHold:     env("AUDIT_ARCHIVE_LEGAL_HOLD", "false") == "true",
		SecretBackend:             env("SECRET_BACKEND", "env"),
		SecretFile:                os.Getenv("SECRET_FILE"),
		SecretEnvPrefix:           env("SECRET_ENV_PREFIX", "PACKAGING_SECRET_"),
		SOPSFile:                  os.Getenv("SOPS_FILE"),
		SOPSCommand:               env("SOPS_COMMAND", "sops"),
		SOPSSecretField:           env("SOPS_SECRET_FIELD", "value"),
		NomadVariablePathPrefix:   env("NOMAD_VARIABLE_PATH_PREFIX", "packaging/secrets"),
		NomadVariableItemKey:      env("NOMAD_VARIABLE_ITEM_KEY", "value"),
		VaultAddr:                 os.Getenv("VAULT_ADDR"),
		VaultToken:                os.Getenv("VAULT_TOKEN"),
		VaultNamespace:            os.Getenv("VAULT_NAMESPACE"),
		VaultKVMount:              env("VAULT_KV_MOUNT", "secret"),
		VaultSecretField:          env("VAULT_SECRET_FIELD", "value"),
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

func envDays(key string, fallbackDays int) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return time.Duration(fallbackDays) * 24 * time.Hour
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 {
		return time.Duration(fallbackDays) * 24 * time.Hour
	}
	if days == 0 {
		return 0
	}
	return time.Duration(days) * 24 * time.Hour
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}
