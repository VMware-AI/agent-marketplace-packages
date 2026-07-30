package server

import (
	_ "embed"
	"net/http"
)

// openapi.json is generated from docs/api/openapi.json by the
// `make openapi-embed` target. CI (and developers) must keep the two
// files byte-identical — `make openapi-check` enforces this.
//
//go:embed openapi.json
var openapiJSON []byte

// HandleOpenAPISpec serves the embedded OpenAPI 3.0.3 spec at
// GET /swagger/openapi.json. Anonymous (mounted outside auth group).
func HandleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openapiJSON)
}

// HandleSwaggerUI serves a self-contained Swagger UI HTML page at
// GET /swagger. Loads the UI JS+CSS from unpkg.com — if you run the
// server behind a network without outbound internet, see the README
// for how to swap in a vendored copy of swagger-ui-dist.
func HandleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>marketplace-api — API docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: "/swagger/openapi.json",
        dom_id: "#swagger-ui",
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis],
      });
    };
  </script>
</body>
</html>
`