package resolver

import (
	"fmt"
	"strings"

	"packaging-platform/backend/internal/apperr"
	"packaging-platform/backend/internal/domain"
)

type OverlayReader interface {
	ReadOverlay(productID string, kind string, name string) (map[string]any, error)
	ReadUpload(id string) (domain.UploadRecord, error)
}

func ResolveBuild(
	product domain.Product,
	request domain.BuildRequest,
	buildID string,
	overlays OverlayReader,
) (map[string]any, []domain.ResolvedComponent, domain.BuildLock, error) {
	config := map[string]any{}
	mergeInto(config, product.Config)

	if target, ok := product.TargetConfig[request.Target]; ok {
		if target.InstallDir != "" {
			setPath(config, []string{"installDir"}, target.InstallDir)
		}
		if target.PackageName != "" {
			setPath(config, []string{"packageName"}, target.PackageName)
		}
		mergeInto(config, target.Config)
	}

	if request.Profile != "" && overlays != nil {
		if overlay, err := overlays.ReadOverlay(product.ID, "profiles", request.Profile); err == nil {
			mergeInto(config, overlay)
		}
	}
	if request.Customer != "" && overlays != nil {
		if overlay, err := overlays.ReadOverlay(product.ID, "customers", request.Customer); err == nil {
			mergeInto(config, overlay)
		}
	}
	mergeInto(config, request.ConfigOverrides)
	mergeInto(config, request.Variables)

	components := normalizeComponents(product)
	resolved := make([]domain.ResolvedComponent, 0, len(components))
	for _, component := range components {
		source := component.Source
		if override, ok := request.ComponentOverrides[component.ID]; ok {
			source = override
		}
		item, err := resolveComponent(component, source, request.Version, overlays)
		if err != nil {
			return nil, nil, domain.BuildLock{}, err
		}
		resolved = append(resolved, item)
	}
	workloads, err := resolveWorkloads(product.Workloads, config, resolved)
	if err != nil {
		return nil, nil, domain.BuildLock{}, err
	}

	lock := domain.BuildLock{
		BuildID:        buildID,
		ProductID:      product.ID,
		Target:         request.Target,
		Profile:        request.Profile,
		Customer:       request.Customer,
		Version:        request.Version,
		ReleaseChannel: request.ReleaseChannel,
		Config:         config,
		Components:     resolved,
		Workloads:      workloads,
	}
	return config, resolved, lock, nil
}

func normalizeComponents(product domain.Product) []domain.Component {
	if len(product.Components) > 0 {
		return product.Components
	}
	components := make([]domain.Component, 0, len(product.Artifacts))
	for id, objectPath := range product.Artifacts {
		components = append(components, domain.Component{
			ID:       id,
			Type:     "artifact",
			Required: true,
			Source: domain.ComponentSource{
				Kind:       "artifact-repository",
				Repository: "minio",
				Path:       objectPath,
			},
		})
	}
	return components
}

func resolveComponent(component domain.Component, source domain.ComponentSource, version string, uploads OverlayReader) (domain.ResolvedComponent, error) {
	uri := source.URI
	sha256 := ""
	if uri == "" {
		switch source.Kind {
		case "artifact-repository", "":
			path := strings.ReplaceAll(source.Path, "{{version}}", version)
			repo := source.Repository
			if repo == "" {
				repo = "minio"
			}
			uri = fmt.Sprintf("%s://%s", repo, strings.TrimLeft(path, "/"))
		case "manual-upload":
			if source.UploadID != "" && uploads != nil {
				record, err := uploads.ReadUpload(source.UploadID)
				if err != nil {
					return domain.ResolvedComponent{}, apperr.Wrap(
						err,
						apperr.KindNotFound,
						"RESOURCE_NOT_FOUND",
						fmt.Sprintf("component %s upload %s not found", component.ID, source.UploadID),
					).WithParam("componentId", component.ID).WithParam("uploadId", source.UploadID)
				}
				uri = record.URI
				sha256 = record.SHA256
				break
			}
			if !component.Required {
				break
			}
			if source.URI == "" {
				return domain.ResolvedComponent{}, apperr.Validation("VALIDATION_REQUIRED_FIELD", fmt.Sprintf("component %s requires upload uri", component.ID)).
					WithField("components.source.uri").
					WithParam("componentId", component.ID)
			}
			uri = source.URI
		case "fixed-url":
			uri = source.URL
		case "previous-build":
			uri = source.URI
		default:
			return domain.ResolvedComponent{}, apperr.Validation("VALIDATION_INVALID_REQUEST", fmt.Sprintf("unsupported source kind %q for component %s", source.Kind, component.ID)).
				WithField("components.source.kind").
				WithParam("kind", source.Kind).
				WithParam("componentId", component.ID)
		}
	}
	return domain.ResolvedComponent{
		ID:       component.ID,
		Type:     component.Type,
		Version:  version,
		Source:   source,
		URI:      uri,
		SHA256:   sha256,
		Required: component.Required,
	}, nil
}

func resolveWorkloads(workloads []domain.Workload, config map[string]any, components []domain.ResolvedComponent) ([]domain.ResolvedWorkload, error) {
	componentIDs := map[string]bool{}
	for _, component := range components {
		componentIDs[component.ID] = true
	}

	resolved := make([]domain.ResolvedWorkload, 0, len(workloads))
	for _, workload := range workloads {
		if workload.ID == "" {
			return nil, apperr.Validation("VALIDATION_REQUIRED_FIELD", "workload id is required").WithField("workloads.id")
		}
		if workload.Runtime == "" {
			return nil, apperr.Validation("VALIDATION_REQUIRED_FIELD", fmt.Sprintf("workload %s runtime is required", workload.ID)).
				WithField("workloads.runtime").
				WithParam("workloadId", workload.ID)
		}
		enabled := workloadEnabled(workload, config)
		if enabled && workload.Component != "" && !componentIDs[workload.Component] {
			return nil, apperr.Validation("VALIDATION_INVALID_REQUEST", fmt.Sprintf("workload %s references unknown component %s", workload.ID, workload.Component)).
				WithField("workloads.component").
				WithParam("workloadId", workload.ID).
				WithParam("componentId", workload.Component)
		}
		resolved = append(resolved, domain.ResolvedWorkload{
			ID:        workload.ID,
			Runtime:   workload.Runtime,
			Component: workload.Component,
			Module:    workload.Module,
			Enabled:   enabled,
			Docker:    workload.Docker,
			Systemd:   workload.Systemd,
		})
	}
	return resolved, nil
}

func workloadEnabled(workload domain.Workload, config map[string]any) bool {
	enabled := true
	if workload.Enabled != nil {
		enabled = *workload.Enabled
	}
	if workload.Module == "" {
		return enabled
	}
	modules, ok := config["modules"].(map[string]any)
	if !ok {
		return enabled
	}
	if value, ok := modules[workload.Module].(bool); ok {
		return enabled && value
	}
	return enabled
}

func mergeInto(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if srcMap, ok := value.(map[string]any); ok {
			if existing, ok := dst[key].(map[string]any); ok {
				mergeInto(existing, srcMap)
				continue
			}
		}
		if strings.Contains(key, ".") {
			setPath(dst, strings.Split(key, "."), value)
			continue
		}
		dst[key] = value
	}
}

func setPath(dst map[string]any, parts []string, value any) {
	current := dst
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}
