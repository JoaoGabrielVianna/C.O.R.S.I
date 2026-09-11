// Command swagger validates the OpenAPI spec at api/openapi/finance.yaml.
//
// On success prints OK and the URL the server will mount the UI on. Used
// by `make swagger` as a pre-commit / CI gate to keep the spec parseable.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	path := "api/openapi/finance.yaml"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(path)
	if err != nil {
		slog.Error("load spec", "path", path, "err", err)
		os.Exit(1)
	}
	if err := doc.Validate(context.Background()); err != nil {
		slog.Error("validate spec", "err", err)
		os.Exit(1)
	}

	fmt.Printf("OK %s — %d paths, %d schemas\n",
		path, len(doc.Paths.Map()), len(doc.Components.Schemas))
	fmt.Println("UI:    http://localhost:8080/swagger")
	fmt.Println("Spec:  http://localhost:8080/openapi.yaml")
}
