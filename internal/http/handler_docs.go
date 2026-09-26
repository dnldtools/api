package http

import (
	_ "embed"
	"encoding/json"
	"net/http"

	apperrors "rest-api/internal/errors"
)

//go:embed assets/openapi.json
var openapiSpec []byte

//go:embed assets/llms.txt
var llmDoc []byte

type docAuthAPIKey struct {
	Token string `json:"token"`
}

type docAuthentication struct {
	PreferredSecurityScheme string         `json:"preferredSecurityScheme"`
	APIKey                  *docAuthAPIKey `json:"apiKey,omitempty"`
}

type docSpec struct {
	URL string `json:"url"`
}

type docConfiguration struct {
	Spec           docSpec            `json:"spec"`
	PageTitle      string             `json:"pageTitle"`
	DarkMode       bool               `json:"darkMode"`
	Authentication *docAuthentication `json:"authentication,omitempty"`
}

const scalarStandaloneJS = "https://cdn.jsdelivr.net/npm/@scalar/api-reference"

func handleDocs(errHandler *ErrorHandler, docsAPIKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			serveStatic(w, "application/json; charset=utf-8", openapiSpec)
			return
		case "/llms.txt", "/llm.txt":
			serveStatic(w, "text/plain; charset=utf-8", llmDoc)
			return
		}

		cfg := docConfiguration{
			Spec:      docSpec{URL: "/openapi.json"},
			PageTitle: "dnld.app Downloader API",
			DarkMode:  true,
		}
		if docsAPIKey != "" {
			cfg.Authentication = &docAuthentication{
				PreferredSecurityScheme: "apiKey",
				APIKey:                  &docAuthAPIKey{Token: docsAPIKey},
			}
		}

		cfgJSON, err := json.Marshal(cfg)
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to render api docs").WithCause(err))
			return
		}

		html := "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>dnld.app Downloader API</title></head><body><script id=\"api-reference\" type=\"application/json\" data-configuration='" + string(cfgJSON) + "'></script><script src=\"" + scalarStandaloneJS + "\"></script></body></html>"

		serveStatic(w, "text/html; charset=utf-8", []byte(html))
	}
}

func serveStatic(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
