package manifest

import (
	"encoding/json"
	"fmt"
	"strings"

	"vela/conductor/internal/domain"
)

var sourceKinds = map[string]bool{
	"artifact-repository": true,
	"manual-upload":       true,
	"fixed-url":           true,
	"previous-build":      true,
}

func ValidateProduct(raw json.RawMessage) (domain.Product, []string) {
	var product domain.Product
	if err := json.Unmarshal(raw, &product); err != nil {
		return domain.Product{}, []string{"JSON 解析失败：" + err.Error()}
	}

	errors := []string{}
	requiredString(&errors, "id", product.ID)
	requiredString(&errors, "name", product.Name)
	if len(product.Targets) == 0 {
		errors = append(errors, "targets 必须至少包含 linux 或 windows")
	}
	seenTargets := map[string]bool{}
	for _, target := range product.Targets {
		if target != "linux" && target != "windows" {
			errors = append(errors, fmt.Sprintf("targets 包含不支持的目标：%s", target))
			continue
		}
		seenTargets[target] = true
		if product.TargetConfig == nil {
			errors = append(errors, fmt.Sprintf("targetConfig.%s 缺失", target))
			continue
		}
		config, ok := product.TargetConfig[target]
		if !ok {
			errors = append(errors, fmt.Sprintf("targetConfig.%s 缺失", target))
			continue
		}
		if strings.TrimSpace(config.InstallDir) == "" {
			errors = append(errors, fmt.Sprintf("targetConfig.%s.installDir 缺失", target))
		}
		if strings.TrimSpace(config.PackageName) == "" {
			errors = append(errors, fmt.Sprintf("targetConfig.%s.packageName 缺失", target))
		}
	}
	if len(product.Profiles) == 0 {
		errors = append(errors, "profiles 必须至少包含一个配置面")
	}
	if len(product.Versions) == 0 {
		errors = append(errors, "versions 必须至少包含一个版本")
	}
	if len(product.Components) == 0 && len(product.Artifacts) == 0 {
		errors = append(errors, "components 或 artifacts 必须至少定义一个组件来源")
	}
	componentIDs := map[string]bool{}
	for index, component := range product.Components {
		prefix := fmt.Sprintf("components[%d]", index)
		if strings.TrimSpace(component.ID) == "" {
			errors = append(errors, prefix+" 缺少 id")
		} else if componentIDs[component.ID] {
			errors = append(errors, prefix+" id 重复："+component.ID)
		} else {
			componentIDs[component.ID] = true
		}
		if strings.TrimSpace(component.Type) == "" {
			errors = append(errors, prefix+" 缺少 type")
		}
		validateSource(&errors, prefix+".source", component.Source)
	}
	for index, workload := range product.Workloads {
		prefix := fmt.Sprintf("workloads[%d]", index)
		if strings.TrimSpace(workload.ID) == "" {
			errors = append(errors, prefix+" 缺少 id")
		}
		if workload.Runtime != "docker" && workload.Runtime != "systemd" {
			errors = append(errors, prefix+" runtime 必须是 docker 或 systemd")
		}
		if workload.Component != "" && len(componentIDs) > 0 && !componentIDs[workload.Component] {
			errors = append(errors, prefix+" 引用了未知组件："+workload.Component)
		}
		if workload.Runtime == "systemd" && workload.Systemd == nil {
			errors = append(errors, prefix+" systemd runtime 需要 systemd 配置")
		}
	}
	if product.Config == nil {
		errors = append(errors, "config 必须是对象")
	}
	_ = seenTargets
	return product, errors
}

func validateSource(errors *[]string, path string, source domain.ComponentSource) {
	if strings.TrimSpace(source.Kind) == "" {
		*errors = append(*errors, path+" 缺少 kind")
		return
	}
	if !sourceKinds[source.Kind] {
		*errors = append(*errors, path+" kind 不支持："+source.Kind)
		return
	}
	switch source.Kind {
	case "artifact-repository":
		if strings.TrimSpace(source.Path) == "" {
			*errors = append(*errors, path+" artifact-repository 需要 path")
		}
	case "fixed-url":
		if strings.TrimSpace(source.URL) == "" {
			*errors = append(*errors, path+" fixed-url 需要 url")
		}
	case "previous-build":
		if strings.TrimSpace(source.URI) == "" {
			*errors = append(*errors, path+" previous-build 需要 uri")
		}
	}
}

func requiredString(errors *[]string, key string, value string) {
	if strings.TrimSpace(value) == "" {
		*errors = append(*errors, "缺少必填字段 "+key)
	}
}
