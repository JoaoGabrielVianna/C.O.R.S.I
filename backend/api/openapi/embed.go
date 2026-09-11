// Package openapi exposes the finance API spec as bytes so the binary can
// serve it without depending on the filesystem at runtime.
package openapi

import _ "embed"

//go:embed finance.yaml
var FinanceSpec []byte
