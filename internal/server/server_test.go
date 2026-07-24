package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kiri-dev/kiri/internal/catalog"
	// Side-effect import registers services for the catalog sanity check.
	_ "github.com/kiri-dev/kiri/internal/registry"
	"github.com/kiri-dev/kiri/internal/service"
)

// TestRootHandler hits the "/" health endpoint of the in-process server and
// verifies the emulator reports it is alive.
func TestRootHandler(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}

	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("expected status ok in body, got %s", body)
	}

	if !strings.Contains(string(body), `"emulator":"kiri"`) {
		t.Fatalf("expected emulator identifier in body, got %s", body)
	}
}

// TestCatalogRendering ensures every registered service implements Describer
// and falls into a known category, so readme-gen never fails in CI.
func TestCatalogRendering(t *testing.T) {
	svcs := service.Services()
	if len(svcs) == 0 {
		t.Fatal("no services registered; check internal/registry import")
	}

	in := "<!-- BEGIN SERVICES -->\n<!-- END SERVICES -->\n## Supported Services (0 services)\n"
	out, err := catalog.Render(in, svcs)
	if err != nil {
		t.Fatalf("catalog.Render: %v", err)
	}

	if strings.Contains(out, "(0 services)") {
		t.Fatalf("catalog count not updated; rendered:\n%s", out)
	}
}
