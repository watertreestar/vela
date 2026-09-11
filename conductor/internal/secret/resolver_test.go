package secret

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"vela/conductor/internal/config"
	"vela/conductor/internal/domain"
)

func TestResolverFindsAndResolvesEnvSecrets(t *testing.T) {
	t.Setenv("PACKAGING_SECRET_DB_ROOT_PASSWORD", "from-env")
	resolver, err := NewResolver(config.Config{
		SecretBackend:   "env",
		SecretEnvPrefix: "PACKAGING_SECRET_",
	})
	if err != nil {
		t.Fatal(err)
	}

	values, err := resolver.ResolveBuild(secretBuild())
	if err != nil {
		t.Fatal(err)
	}
	if values["PACKAGING_SECRET_DB_ROOT_PASSWORD"] != "from-env" {
		t.Fatalf("secret not resolved: %#v", values)
	}
}

func TestResolverReadsFileSecrets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(file, []byte(`{"db/root-password":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(config.Config{
		SecretBackend:   "file",
		SecretFile:      file,
		SecretEnvPrefix: "PACKAGING_SECRET_",
	})
	if err != nil {
		t.Fatal(err)
	}

	values, err := resolver.ResolveBuild(secretBuild())
	if err != nil {
		t.Fatal(err)
	}
	if values["PACKAGING_SECRET_DB_ROOT_PASSWORD"] != "from-file" {
		t.Fatalf("file secret not resolved: %#v", values)
	}
}

func TestResolverReadsSOPSSecrets(t *testing.T) {
	original := runSOPSDecrypt
	t.Cleanup(func() { runSOPSDecrypt = original })
	var gotCommand string
	var gotFile string
	runSOPSDecrypt = func(command string, file string) ([]byte, error) {
		gotCommand = command
		gotFile = file
		return []byte(`{"db":{"root-password":{"value":"from-sops"}},"db/root-password":"from-exact"}`), nil
	}

	resolver, err := NewResolver(config.Config{
		SecretBackend:   "sops",
		SecretEnvPrefix: "PACKAGING_SECRET_",
		SOPSFile:        "/etc/packaging/secrets.enc.json",
		SOPSCommand:     "sops-test",
		SOPSSecretField: "value",
	})
	if err != nil {
		t.Fatal(err)
	}

	values, err := resolver.ResolveBuild(secretBuild())
	if err != nil {
		t.Fatal(err)
	}
	if values["PACKAGING_SECRET_DB_ROOT_PASSWORD"] != "from-exact" {
		t.Fatalf("sops secret not resolved by exact key precedence: %#v", values)
	}
	if gotCommand != "sops-test" || gotFile != "/etc/packaging/secrets.enc.json" {
		t.Fatalf("unexpected sops invocation: command=%q file=%q", gotCommand, gotFile)
	}
}

func TestResolverReadsNestedSOPSSecretField(t *testing.T) {
	original := runSOPSDecrypt
	t.Cleanup(func() { runSOPSDecrypt = original })
	runSOPSDecrypt = func(command string, file string) ([]byte, error) {
		return []byte(`{"db":{"root-password":{"secret":"from-nested-sops"}}}`), nil
	}

	resolver, err := NewResolver(config.Config{
		SecretBackend:   "sops",
		SecretEnvPrefix: "PACKAGING_SECRET_",
		SOPSFile:        "/etc/packaging/secrets.enc.json",
		SOPSSecretField: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	values, err := resolver.ResolveBuild(secretBuild())
	if err != nil {
		t.Fatal(err)
	}
	if values["PACKAGING_SECRET_DB_ROOT_PASSWORD"] != "from-nested-sops" {
		t.Fatalf("nested sops secret not resolved: %#v", values)
	}
}

func TestResolverFailsForMissingSecret(t *testing.T) {
	resolver, err := NewResolver(config.Config{
		SecretBackend:   "env",
		SecretEnvPrefix: "PACKAGING_SECRET_",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveBuild(secretBuild()); err == nil {
		t.Fatalf("expected missing secret error")
	}
}

func TestResolverReadsVaultKV2Secrets(t *testing.T) {
	var gotPath string
	var gotToken string
	var gotNamespace string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Vault-Token")
		gotNamespace = r.Header.Get("X-Vault-Namespace")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"data":{"value":"from-vault"}}}`))
	}))
	defer server.Close()

	resolver, err := NewResolver(config.Config{
		SecretBackend:    "vault",
		SecretEnvPrefix:  "PACKAGING_SECRET_",
		VaultAddr:        server.URL,
		VaultToken:       "vault-token",
		VaultNamespace:   "team-a",
		VaultKVMount:     "kv",
		VaultSecretField: "value",
	})
	if err != nil {
		t.Fatal(err)
	}

	values, err := resolver.ResolveBuild(secretBuild())
	if err != nil {
		t.Fatal(err)
	}
	if values["PACKAGING_SECRET_DB_ROOT_PASSWORD"] != "from-vault" {
		t.Fatalf("vault secret not resolved: %#v", values)
	}
	if gotPath != "/v1/kv/data/db/root-password" {
		t.Fatalf("unexpected vault path: %s", gotPath)
	}
	if gotToken != "vault-token" || gotNamespace != "team-a" {
		t.Fatalf("vault headers missing: token=%q namespace=%q", gotToken, gotNamespace)
	}
}

func TestResolverReadsNomadVariableSecrets(t *testing.T) {
	var gotPath string
	var gotToken string
	var gotNamespace string
	var gotRegion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Nomad-Token")
		gotNamespace = r.URL.Query().Get("namespace")
		gotRegion = r.URL.Query().Get("region")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Items":{"value":"from-nomad"}}`))
	}))
	defer server.Close()

	resolver, err := NewResolver(config.Config{
		SecretBackend:           "nomad",
		SecretEnvPrefix:         "PACKAGING_SECRET_",
		NomadAddr:               server.URL,
		NomadToken:              "nomad-token",
		NomadNamespace:          "packaging",
		NomadRegion:             "global",
		NomadVariablePathPrefix: "platform/secrets",
		NomadVariableItemKey:    "value",
	})
	if err != nil {
		t.Fatal(err)
	}

	values, err := resolver.ResolveBuild(secretBuild())
	if err != nil {
		t.Fatal(err)
	}
	if values["PACKAGING_SECRET_DB_ROOT_PASSWORD"] != "from-nomad" {
		t.Fatalf("nomad variable secret not resolved: %#v", values)
	}
	if gotPath != "/v1/var/platform/secrets/db/root-password" {
		t.Fatalf("unexpected nomad variable path: %s", gotPath)
	}
	if gotToken != "nomad-token" || gotNamespace != "packaging" || gotRegion != "global" {
		t.Fatalf("nomad request context missing: token=%q namespace=%q region=%q", gotToken, gotNamespace, gotRegion)
	}
}

func TestResolverVaultRequiresConfiguration(t *testing.T) {
	if _, err := NewResolver(config.Config{SecretBackend: "vault"}); err == nil {
		t.Fatalf("expected missing vault config error")
	}
	if _, err := NewResolver(config.Config{SecretBackend: "vault", VaultAddr: "http://vault.test"}); err == nil {
		t.Fatalf("expected missing vault token error")
	}
}

func TestResolverNomadRequiresConfiguration(t *testing.T) {
	if _, err := NewResolver(config.Config{SecretBackend: "nomad"}); err == nil {
		t.Fatalf("expected missing nomad config error")
	}
}

func TestResolverSOPSRequiresConfiguration(t *testing.T) {
	if _, err := NewResolver(config.Config{SecretBackend: "sops"}); err == nil {
		t.Fatalf("expected missing sops config error")
	}
}

func TestSecretRefsAreStableAndDeduplicated(t *testing.T) {
	refs := SecretRefs(secretBuild())
	if len(refs) != 1 || refs[0] != "db/root-password" {
		t.Fatalf("unexpected refs: %#v", refs)
	}
	if EnvName("db/root-password", "PACKAGING_SECRET_") != "PACKAGING_SECRET_DB_ROOT_PASSWORD" {
		t.Fatalf("unexpected env name")
	}
}

func secretBuild() domain.Build {
	return domain.Build{
		ID:        "build-secret",
		ProductID: "lsap",
		Target:    "linux",
		FinalConfig: map[string]any{
			"database": map[string]any{
				"rootPassword": "secret:db/root-password",
			},
		},
		Workloads: []domain.ResolvedWorkload{{
			ID:      "api",
			Runtime: "docker",
			Docker: map[string]any{
				"environment": map[string]any{
					"MYSQL_ROOT_PASSWORD": "secret://db/root-password",
				},
			},
			Enabled: true,
		}},
	}
}
