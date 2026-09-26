package http

import (
	_ "embed"
	"net/http"

	apperrors "rest-api/internal/errors"

	scalar "github.com/MarceloPetrucio/go-scalar-api-reference"
)

//go:embed assets/openapi.json
var openapiSpec []byte

//go:embed assets/llm.txt
var llmDoc []byte

func handleDocs(errHandler *ErrorHandler) http.HandlerFunc {
	spec := string(openapiSpec)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(openapiSpec)
			return
		}

		if r.URL.Path == "/llm.txt" || r.URL.Path == "/llms.txt" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(llmDoc)
			return
		}

		html, err := scalar.ApiReferenceHTML(&scalar.Options{
			SpecContent: spec,
			CustomOptions: scalar.CustomOptions{
				PageTitle: "dnld.app Downloader API",
			},
			DarkMode: true,
		})
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to render api docs").WithCause(err))
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}
}
