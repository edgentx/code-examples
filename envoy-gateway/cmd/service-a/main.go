// Command service-a is the first upstream behind the gateway. It answers
// everything the gateway routes to /api/a, and it has no slow endpoint: the
// timeout demonstration belongs to service-b.
package main

import (
	"cmp"
	"log"
	"os"

	"github.com/edgentx/code-examples/envoy-gateway/internal/echo"
)

func main() {
	config := echo.Config{
		Service: "service-a",
		// LISTEN_ADDR lets docker-compose place the service without a rebuild.
		Addr: cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080"),
	}
	if err := echo.ListenAndServe(config); err != nil {
		log.Fatalf("service-a: %v", err)
	}
}
