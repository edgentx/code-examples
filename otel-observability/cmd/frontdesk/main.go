// Command frontdesk is the citizen-facing service. It answers
// GET /requests/{caseID} by calling the records office over HTTP.
package main

import (
	"log"
	"net/http"

	"github.com/edgentx/code-examples/otel-observability/internal/service"
)

func main() {
	addr := service.Env("FRONTDESK_ADDR", ":8200")
	recordsBaseURL := service.Env("RECORDS_BASE_URL", "http://localhost:8201")

	err := service.Run("frontdesk", addr, func(deps service.Deps) (http.Handler, error) {
		return service.NewFrontDesk(deps, recordsBaseURL)
	})
	if err != nil {
		log.Fatalf("frontdesk: %v", err)
	}
}
