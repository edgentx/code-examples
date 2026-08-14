// Command records is the downstream service. It answers GET /records/{caseID}
// from a synthetic case-file catalog.
package main

import (
	"log"
	"net/http"

	"github.com/edgentx/code-examples/otel-observability/internal/service"
)

func main() {
	addr := service.Env("RECORDS_ADDR", ":8201")

	err := service.Run("records", addr, func(deps service.Deps) (http.Handler, error) {
		return service.NewRecords(deps)
	})
	if err != nil {
		log.Fatalf("records: %v", err)
	}
}
