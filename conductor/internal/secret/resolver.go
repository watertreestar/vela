package secret

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"vela/conductor/internal/config"
	"vela/conductor/internal/domain"
)

var envNamePattern = regexp.MustCompile(`[^A-Z0-9]+`)

var runSOPSDecrypt = func(command string, file string) ([]byte, error) {
	cmd := exec.Command(command, "--decrypt", "--output-type", "json", file)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail := strings.TrimSpace(string(exitErr.Stderr))
			if detail != "" {
				return nil, fmt.Errorf("sops decrypt failed: %s", detail)
			}
		}
		return nil, err
	}
	return output, nil
}

type Resolver struct {
	cfg        config.Config
	file       map[string]string
	sops       map[string]any
	httpClient *http.Client
}

func NewResolver(cfg config.Config) (*Resolver, error) {
	resolver := &Resolver{cfg: cfg}
	if resolver.cfg.SecretEnvPrefix == "" {
		resolver.cfg.SecretEnvPrefix = "PACKAGING_SECRET_"
	}
	switch strings.ToLower(strings.TrimSpace(resolver.cfg.SecretBackend)) {
	case "", "env":
		return resolver, nil
	case "file":
		if resolver.cfg.SecretFile == "" {
			return nil, fmt.Errorf("SECRET_BACKEND=file requires SECRET_FILE")
		}
		content, err := os.ReadFile(resolver.cfg.SecretFile)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(content, &resolver.file); err != nil {
			return nil, fmt.Errorf("read secret file: %w", err)
		}
		return resolver, nil
	case "sops":
		if strings.TrimSpace(resolver.cfg.SOPSFile) == "" {
			return nil, fmt.Errorf("SECRET_BACKEND=sops requires SOPS_FILE")
		}
		if strings.TrimSpace(resolver.cfg.SOPSCommand) == "" {
			resolver.cfg.SOPSCommand = "sops"
		}
		if strings.TrimSpace(resolver.cfg.SOPSSecretField) == "" {
			resolver.cfg.SOPSSecretField = "value"
		}
		content, err := runSOPSDecrypt(resolver.cfg.SOPSCommand, resolver.cfg.SOPSFile)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(content, &resolver.sops); err != nil {
			return nil, fmt.Errorf("read SOPS secret file: %w", err)
		}
		return resolver, nil
	case "vault", "vault-kv2":
		if strings.TrimSpace(resolver.cfg.VaultAddr) == "" {
			return nil, fmt.Errorf("SECRET_BACKEND=vault requires VAULT_ADDR")
		}
		if strings.TrimSpace(resolver.cfg.VaultToken) == "" {
			return nil, fmt.Errorf("SECRET_BACKEND=vault requires VAULT_TOKEN")
		}
		if strings.TrimSpace(resolver.cfg.VaultKVMount) == "" {
			resolver.cfg.VaultKVMount = "secret"
		}
		if strings.TrimSpace(resolver.cfg.VaultSecretField) == "" {
			resolver.cfg.VaultSecretField = "value"
		}
		resolver.httpClient = http.DefaultClient
		return resolver, nil
	case "nomad", "nomad-var", "nomad-variables":
		if strings.TrimSpace(resolver.cfg.NomadAddr) == "" {
			return nil, fmt.Errorf("SECRET_BACKEND=nomad requires NOMAD_ADDR")
		}
		if strings.TrimSpace(resolver.cfg.NomadVariablePathPrefix) == "" {
			resolver.cfg.NomadVariablePathPrefix = "packaging/secrets"
		}
		if strings.TrimSpace(resolver.cfg.NomadVariableItemKey) == "" {
			resolver.cfg.NomadVariableItemKey = "value"
		}
		resolver.httpClient = http.DefaultClient
		return resolver, nil
	case "disabled", "none":
		return resolver, nil
	default:
		return nil, fmt.Errorf("unsupported SECRET_BACKEND %q", resolver.cfg.SecretBackend)
	}
}

func (r Resolver) ResolveBuild(build domain.Build) (map[string]string, error) {
	refs := SecretRefs(build)
	resolved := map[string]string{}
	for _, ref := range refs {
		value, err := r.lookup(ref)
		if err != nil {
			return nil, err
		}
		resolved[EnvName(ref, r.cfg.SecretEnvPrefix)] = value
	}
	return resolved, nil
}

func (r Resolver) lookup(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(r.cfg.SecretBackend)) {
	case "disabled", "none":
		return "", fmt.Errorf("secret %q is not configured", name)
	case "file":
		value, ok := r.file[name]
		if !ok {
			return "", fmt.Errorf("secret %q is not configured", name)
		}
		return value, nil
	case "vault", "vault-kv2":
		return r.lookupVault(name)
	case "nomad", "nomad-var", "nomad-variables":
		return r.lookupNomadVariable(name)
	case "sops":
		return r.lookupSOPS(name)
	default:
		value, ok := os.LookupEnv(EnvName(name, r.cfg.SecretEnvPrefix))
		if !ok {
			return "", fmt.Errorf("secret %q is not configured", name)
		}
		return value, nil
	}
}

func (r Resolver) lookupVault(name string) (string, error) {
	base, err := url.Parse(strings.TrimRight(r.cfg.VaultAddr, "/"))
	if err != nil {
		return "", err
	}
	secretURL := base.JoinPath("v1", strings.Trim(r.cfg.VaultKVMount, "/"), "data", strings.Trim(name, "/"))
	request, err := http.NewRequest(http.MethodGet, secretURL.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("X-Vault-Token", r.cfg.VaultToken)
	if namespace := strings.TrimSpace(r.cfg.VaultNamespace); namespace != "" {
		request.Header.Set("X-Vault-Namespace", namespace)
	}
	client := r.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("vault secret %q not found", name)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("vault secret %q returned HTTP %d", name, response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	var payload struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	field := strings.TrimSpace(r.cfg.VaultSecretField)
	if field == "" {
		field = "value"
	}
	value, ok := payload.Data.Data[field]
	if !ok {
		return "", fmt.Errorf("vault secret %q field %q not found", name, field)
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return "", fmt.Errorf("vault secret %q field %q is empty", name, field)
		}
		return typed, nil
	default:
		return fmt.Sprint(typed), nil
	}
}

func (r Resolver) lookupNomadVariable(name string) (string, error) {
	base, err := url.Parse(strings.TrimRight(r.cfg.NomadAddr, "/"))
	if err != nil {
		return "", err
	}
	variableURL := base.JoinPath("v1", "var", strings.Trim(r.cfg.NomadVariablePathPrefix, "/"), strings.Trim(name, "/"))
	query := variableURL.Query()
	if namespace := strings.TrimSpace(r.cfg.NomadNamespace); namespace != "" {
		query.Set("namespace", namespace)
	}
	if region := strings.TrimSpace(r.cfg.NomadRegion); region != "" {
		query.Set("region", region)
	}
	variableURL.RawQuery = query.Encode()
	request, err := http.NewRequest(http.MethodGet, variableURL.String(), nil)
	if err != nil {
		return "", err
	}
	if token := strings.TrimSpace(r.cfg.NomadToken); token != "" {
		request.Header.Set("X-Nomad-Token", token)
	}
	client := r.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("nomad variable %q not found", name)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("nomad variable %q returned HTTP %d", name, response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	var payload struct {
		Items map[string]string `json:"Items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	itemKey := strings.TrimSpace(r.cfg.NomadVariableItemKey)
	if itemKey == "" {
		itemKey = "value"
	}
	value, ok := payload.Items[itemKey]
	if !ok {
		return "", fmt.Errorf("nomad variable %q item %q not found", name, itemKey)
	}
	if value == "" {
		return "", fmt.Errorf("nomad variable %q item %q is empty", name, itemKey)
	}
	return value, nil
}

func (r Resolver) lookupSOPS(name string) (string, error) {
	value, ok := structuredSecretValue(r.sops, name)
	if !ok {
		return "", fmt.Errorf("sops secret %q is not configured", name)
	}
	result, err := secretScalarValue("sops secret", name, strings.TrimSpace(r.cfg.SOPSSecretField), value)
	if err != nil {
		return "", err
	}
	return result, nil
}

func structuredSecretValue(root map[string]any, name string) (any, bool) {
	if root == nil {
		return nil, false
	}
	if value, ok := root[name]; ok {
		return value, true
	}
	var current any = root
	for _, part := range strings.Split(strings.Trim(name, "/"), "/") {
		if part == "" {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = next
		case map[string]string:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = next
		default:
			return nil, false
		}
	}
	return current, true
}

func secretScalarValue(source string, name string, field string, value any) (string, error) {
	if field == "" {
		field = "value"
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return "", fmt.Errorf("%s %q is empty", source, name)
		}
		return typed, nil
	case map[string]any:
		nested, ok := typed[field]
		if !ok {
			return "", fmt.Errorf("%s %q field %q not found", source, name, field)
		}
		return secretScalarValue(source, name, field, nested)
	case map[string]string:
		nested, ok := typed[field]
		if !ok {
			return "", fmt.Errorf("%s %q field %q not found", source, name, field)
		}
		return secretScalarValue(source, name, field, nested)
	default:
		return fmt.Sprint(typed), nil
	}
}

func SecretRefs(build domain.Build) []string {
	refs := map[string]bool{}
	collect(build.FinalConfig, refs)
	collect(build.Variables, refs)
	collect(build.Workloads, refs)
	collect(build.BuildLock.Config, refs)
	collect(build.BuildLock.Workloads, refs)
	result := make([]string, 0, len(refs))
	for ref := range refs {
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

func collect(value any, refs map[string]bool) {
	collectDepth(value, refs, 0)
}

func collectDepth(value any, refs map[string]bool, depth int) {
	if value == nil || depth > 32 {
		return
	}
	switch typed := value.(type) {
	case string:
		if ref, ok := ParseRef(typed); ok {
			refs[ref] = true
		}
	case map[string]any:
		for _, child := range typed {
			collectDepth(child, refs, depth+1)
		}
	case map[string]string:
		for _, child := range typed {
			collectDepth(child, refs, depth+1)
		}
	case []any:
		for _, child := range typed {
			collectDepth(child, refs, depth+1)
		}
	case bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64,
		json.Number:
		return
	default:
		content, err := json.Marshal(typed)
		if err != nil || string(content) == "null" {
			return
		}
		var decoded any
		if err := json.Unmarshal(content, &decoded); err != nil {
			return
		}
		switch decoded.(type) {
		case string, map[string]any, []any:
			collectDepth(decoded, refs, depth+1)
		}
	}
}

func ParseRef(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(trimmed, "secret://"):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "secret://"))
		return name, name != ""
	case strings.HasPrefix(trimmed, "secret:"):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "secret:"))
		return name, name != ""
	default:
		return "", false
	}
}

func EnvName(name string, prefix string) string {
	if prefix == "" {
		prefix = "PACKAGING_SECRET_"
	}
	normalized := strings.ToUpper(strings.TrimSpace(name))
	normalized = envNamePattern.ReplaceAllString(normalized, "_")
	normalized = strings.Trim(normalized, "_")
	return prefix + normalized
}
