package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var contractRoutes = map[string][]string{
	"/v1/downloads":           {"post"},
	"/v1/youtube/search":      {"get"},
	"/v1/youtube/formats":     {"get"},
	"/v1/youtube/convert":     {"post"},
	"/v1/account":             {"get"},
	"/v1/usage":               {"get"},
	"/v1/keys":                {"get", "post"},
	"/v1/keys/{id}/revoke":    {"post"},
	"/v1/admin/accounts":      {"get"},
	"/v1/admin/accounts/{id}": {"get", "patch", "delete"},
	"/v1/admin/keys":          {"get", "post"},
	"/v1/admin/keys/{id}":     {"get", "patch", "delete"},
	"/v1/admin/stats":         {"get"},
}

type openAPIDocument struct {
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths map[string]map[string]struct {
		XStatus  string                `yaml:"x-status"`
		Security []map[string][]string `yaml:"security"`
	} `yaml:"paths"`
	Components struct {
		SecuritySchemes map[string]struct {
			Type string `yaml:"type"`
			In   string `yaml:"in"`
			Name string `yaml:"name"`
		} `yaml:"securitySchemes"`
	} `yaml:"components"`
}

func loadOpenAPIDoc(t *testing.T) openAPIDocument {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "openapi.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	var doc openAPIDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	return doc
}

func TestOpenAPIProductionBaseURL(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	if len(doc.Servers) == 0 {
		t.Fatal("openapi.yaml has no servers")
	}
	if got := doc.Servers[0].URL; got != "https://api.dnld.app" {
		t.Errorf("primary server url = %q, want https://api.dnld.app", got)
	}
}

func TestOpenAPIPathsMatchImplementedRoutes(t *testing.T) {
	doc := loadOpenAPIDoc(t)

	if len(doc.Paths) != len(contractRoutes) {
		t.Errorf("openapi.yaml documents %d paths, want %d", len(doc.Paths), len(contractRoutes))
	}

	router := newTestRouter(t)
	for path, methods := range contractRoutes {
		if strings.HasPrefix(path, "/api/") {
			t.Errorf("path %q must not use the /api prefix", path)
		}
		docMethods, ok := doc.Paths[path]
		if !ok {
			t.Errorf("openapi.yaml is missing path %q", path)
			continue
		}
		for _, method := range methods {
			if _, ok := docMethods[method]; !ok {
				t.Errorf("openapi.yaml path %q is missing method %q", path, method)
				continue
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(strings.ToUpper(method), path, nil)
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound {
				t.Errorf("%s %s is documented but returns 404 from the router", strings.ToUpper(method), path)
			}
		}
	}
}

func TestOpenAPINoAPIPrefix(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	for path := range doc.Paths {
		if strings.HasPrefix(path, "/api/") || path == "/api" {
			t.Errorf("openapi.yaml path %q must not use the /api prefix", path)
		}
	}
}

func TestOpenAPIEndpointStatusAnnotations(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	want := map[string]map[string]string{
		"/v1/downloads":           {"post": "skeleton"},
		"/v1/youtube/search":      {"get": "implemented"},
		"/v1/youtube/formats":     {"get": "implemented"},
		"/v1/youtube/convert":     {"post": "implemented"},
		"/v1/account":             {"get": "implemented"},
		"/v1/usage":               {"get": "implemented"},
		"/v1/keys":                {"get": "implemented", "post": "implemented"},
		"/v1/keys/{id}/revoke":    {"post": "implemented"},
		"/v1/admin/accounts":      {"get": "implemented"},
		"/v1/admin/accounts/{id}": {"get": "implemented", "patch": "implemented", "delete": "implemented"},
		"/v1/admin/keys":          {"get": "implemented", "post": "implemented"},
		"/v1/admin/keys/{id}":     {"get": "implemented", "patch": "implemented", "delete": "implemented"},
		"/v1/admin/stats":         {"get": "implemented"},
	}
	for path, methods := range contractRoutes {
		for _, method := range methods {
			op, ok := doc.Paths[path][method]
			if !ok {
				continue
			}
			if op.XStatus != want[path][method] {
				t.Errorf("%s %s x-status = %q, want %q", method, path, op.XStatus, want[path][method])
			}
		}
	}
}

func TestOpenAPIAPIDocumentsAPIKeyIdentification(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	scheme, ok := doc.Components.SecuritySchemes["ApiKeyAuth"]
	if !ok {
		t.Fatal("openapi.yaml is missing the ApiKeyAuth security scheme")
	}
	if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != "X-API-Key" {
		t.Errorf("ApiKeyAuth = {%s %s %s}, want {apiKey header X-API-Key}", scheme.Type, scheme.In, scheme.Name)
	}
}

func TestOpenAPIProtectedEndpointsRequireAPIKey(t *testing.T) {
	doc := loadOpenAPIDoc(t)

	protected := map[string][]string{
		"/v1/downloads":           {"post"},
		"/v1/youtube/search":      {"get"},
		"/v1/youtube/formats":     {"get"},
		"/v1/youtube/convert":     {"post"},
		"/v1/account":             {"get"},
		"/v1/usage":               {"get"},
		"/v1/keys":                {"get", "post"},
		"/v1/keys/{id}/revoke":    {"post"},
		"/v1/admin/accounts":      {"get"},
		"/v1/admin/accounts/{id}": {"get", "patch", "delete"},
		"/v1/admin/keys":          {"get", "post"},
		"/v1/admin/keys/{id}":     {"get", "patch", "delete"},
		"/v1/admin/stats":         {"get"},
	}
	for path, methods := range protected {
		for _, method := range methods {
			op := doc.Paths[path][method]
			if len(op.Security) == 0 {
				t.Errorf("%s %s must declare the ApiKeyAuth security scheme", method, path)
				continue
			}
			found := false
			for _, req := range op.Security {
				if _, ok := req["ApiKeyAuth"]; ok {
					found = true
				}
			}
			if !found {
				t.Errorf("%s %s security must reference ApiKeyAuth", method, path)
			}
		}
	}
}
