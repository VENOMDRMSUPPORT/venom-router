package tray

import (
	"path/filepath"
	"testing"
)

func TestCatalogGroupSeparatesProductionAndDevelopment(t *testing.T) {
	root := filepath.Join("C:", "repo", "catalog")
	prod := catalogGroup("catalog.production", "production", root, filepath.Join(root, "data", "catalog.db"), catalogProductionAPI, catalogProductionUI, "prod.log", false)
	dev := catalogGroup("catalog.development", "development", root, filepath.Join("C:", "data", "catalog-development.db"), catalogDevelopmentAPI, catalogDevelopmentUI, "dev.log", true)

	if prod.Services[0].Spec.Args[len(prod.Services[0].Spec.Args)-1] != "--port=8791" {
		t.Fatalf("production API args = %v, want port 8791", prod.Services[0].Spec.Args)
	}
	if dev.Services[0].Spec.Args[len(dev.Services[0].Spec.Args)-1] != "--port=8792" {
		t.Fatalf("development API args = %v, want port 8792", dev.Services[0].Spec.Args)
	}
	if prod.Services[0].DataRoot == dev.Services[0].DataRoot {
		t.Fatal("production and development Catalog data roots must differ")
	}
	if prod.Services[1].Spec.Env[0] == dev.Services[1].Spec.Env[0] {
		t.Fatal("production and development Catalog API targets must differ")
	}
}
