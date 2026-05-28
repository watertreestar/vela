package resolver

import (
	"testing"
	"time"

	"packaging-platform/backend/internal/domain"
)

type fakeOverlayStore struct {
	overlays map[string]map[string]any
	uploads  map[string]domain.UploadRecord
}

func (s fakeOverlayStore) ReadOverlay(productID string, kind string, name string) (map[string]any, error) {
	return s.overlays[kind+"/"+name], nil
}

func (s fakeOverlayStore) ReadUpload(id string) (domain.UploadRecord, error) {
	return s.uploads[id], nil
}

func TestResolveBuildMergesConfigAndLocksComponents(t *testing.T) {
	product := domain.Product{
		ID:       "lsap",
		Profiles: []string{"qa"},
		TargetConfig: map[string]domain.TargetConfig{
			"linux": {
				InstallDir: "/opt/lsap",
				Config: map[string]any{
					"runtime": "docker-compose",
				},
			},
		},
		Config: map[string]any{
			"ports": map[string]any{"web": float64(48000)},
			"modules": map[string]any{
				"camera": true,
				"push":   true,
			},
		},
		Components: []domain.Component{{
			ID:       "engine",
			Type:     "archive",
			Required: true,
			Source: domain.ComponentSource{
				Kind:       "artifact-repository",
				Repository: "minio",
				Path:       "artifact/engine/{{version}}/engine.tar.gz",
			},
		}},
	}
	store := fakeOverlayStore{
		overlays: map[string]map[string]any{
			"profiles/qa": {
				"ports": map[string]any{"web": float64(49000)},
			},
		},
	}

	config, components, lock, err := ResolveBuild(product, domain.BuildRequest{
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.2.3",
		ConfigOverrides: map[string]any{
			"modules.camera": false,
		},
	}, "build-1", store)
	if err != nil {
		t.Fatalf("ResolveBuild returned error: %v", err)
	}

	if config["installDir"] != "/opt/lsap" {
		t.Fatalf("installDir not merged: %#v", config["installDir"])
	}
	if config["runtime"] != "docker-compose" {
		t.Fatalf("target config not merged: %#v", config["runtime"])
	}
	if config["ports"].(map[string]any)["web"] != float64(49000) {
		t.Fatalf("profile overlay did not win: %#v", config["ports"])
	}
	if config["modules"].(map[string]any)["camera"] != false {
		t.Fatalf("dot override did not win: %#v", config["modules"])
	}
	if components[0].URI != "minio://artifact/engine/1.2.3/engine.tar.gz" {
		t.Fatalf("component uri not locked: %s", components[0].URI)
	}
	if lock.BuildID != "build-1" || lock.Components[0].URI != components[0].URI {
		t.Fatalf("build lock mismatch: %#v", lock)
	}
}

func TestResolveBuildUsesManualUploadRecord(t *testing.T) {
	product := domain.Product{
		ID: "lsap",
		Components: []domain.Component{{
			ID:       "license",
			Type:     "license",
			Required: true,
			Source: domain.ComponentSource{
				Kind: "manual-upload",
			},
		}},
	}
	store := fakeOverlayStore{
		uploads: map[string]domain.UploadRecord{
			"upload-1": {
				ID:        "upload-1",
				URI:       "local-upload://upload-1/license.lic",
				SHA256:    "abc",
				CreatedAt: time.Now(),
			},
		},
	}

	_, components, _, err := ResolveBuild(product, domain.BuildRequest{
		ProductID: "lsap",
		Target:    "linux",
		Version:   "latest",
		ComponentOverrides: map[string]domain.ComponentSource{
			"license": {
				Kind:     "manual-upload",
				UploadID: "upload-1",
			},
		},
	}, "build-1", store)
	if err != nil {
		t.Fatalf("ResolveBuild returned error: %v", err)
	}
	if components[0].URI != "local-upload://upload-1/license.lic" {
		t.Fatalf("upload uri not resolved: %#v", components[0])
	}
	if components[0].SHA256 != "abc" {
		t.Fatalf("upload sha not resolved: %#v", components[0])
	}
}

func TestResolveBuildResolvesWorkloadsFromModuleFlags(t *testing.T) {
	enabled := true
	product := domain.Product{
		ID: "lsap",
		Config: map[string]any{
			"modules": map[string]any{
				"camera": false,
				"push":   true,
			},
		},
		Components: []domain.Component{{
			ID:       "worker",
			Type:     "binary",
			Required: true,
			Source: domain.ComponentSource{
				Kind: "fixed-url",
				URL:  "https://example.invalid/worker",
			},
		}},
		Workloads: []domain.Workload{
			{
				ID:        "worker",
				Runtime:   "systemd",
				Component: "worker",
				Module:    "camera",
				Enabled:   &enabled,
				Systemd: &domain.SystemdWorkload{
					UnitName:  "worker.service",
					ExecStart: "/opt/worker",
				},
			},
			{
				ID:        "push",
				Runtime:   "docker",
				Component: "worker",
				Module:    "push",
			},
		},
	}

	_, _, lock, err := ResolveBuild(product, domain.BuildRequest{
		ProductID: "lsap",
		Target:    "linux",
		Version:   "latest",
	}, "build-1", nil)
	if err != nil {
		t.Fatalf("ResolveBuild returned error: %v", err)
	}
	if lock.Workloads[0].Enabled {
		t.Fatalf("camera workload should be disabled by module flag: %#v", lock.Workloads[0])
	}
	if !lock.Workloads[1].Enabled {
		t.Fatalf("push workload should be enabled: %#v", lock.Workloads[1])
	}
}
