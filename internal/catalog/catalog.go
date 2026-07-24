// Package catalog renders the README "Supported Services" section from the
// registered services. Each service's Meta() is the single source of truth;
// cmd/readme-gen writes the README and a test verifies it read-only.
package catalog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Brilhante29/kiri-gcp/internal/service"
)

// CategoryOrder defines the section order of the generated catalog. Every
// service's Meta().Category must appear here; an unknown category is an error.
var CategoryOrder = []string{
	"Storage",
	"Compute",
	"Containers",
	"Databases",
	"Analytics & ML",
	"Messaging & Integration",
	"Application Integration",
	"Security",
	"Networking",
	"Monitoring & Logging",
	"Management & Billing",
	"Developer Tools",
	"Other Services",
}

var (
	beginMarkerRe = regexp.MustCompile(`(?m)^<!-- BEGIN SERVICES.*-->$`)
	endMarker     = "<!-- END SERVICES -->"
	countRe       = regexp.MustCompile(`## Supported Services \(\d+ services\)`)
)

// Render replaces the catalog region (between the BEGIN/END markers) and the
// "(N services)" count in content. It is pure and does not touch the filesystem.
func Render(content string, services []service.Service) (string, error) {
	body, count, err := generate(services)
	if err != nil {
		return "", err
	}

	return apply(content, body, count)
}

func generate(services []service.Service) (string, int, error) {
	known := make(map[string]bool, len(CategoryOrder))
	for _, c := range CategoryOrder {
		known[c] = true
	}

	byCategory := make(map[string][]service.Meta)

	for _, svc := range services {
		d, ok := svc.(service.Describer)
		if !ok {
			return "", 0, fmt.Errorf("service %q does not implement service.Describer (add a Meta() method)", svc.Name())
		}

		meta := d.Meta()
		if !known[meta.Category] {
			return "", 0, fmt.Errorf("service %q has unknown category %q; add it to catalog.CategoryOrder", svc.Name(), meta.Category)
		}

		byCategory[meta.Category] = append(byCategory[meta.Category], meta)
	}

	sections := make([]string, 0, len(CategoryOrder))

	for _, cat := range CategoryOrder {
		metas := byCategory[cat]
		if len(metas) == 0 {
			continue
		}

		sort.Slice(metas, func(i, j int) bool { return metas[i].Display < metas[j].Display })

		var b strings.Builder

		fmt.Fprintf(&b, "### %s\n", cat)
		b.WriteString("| Service | Description | Fidelity | State |\n")
		b.WriteString("|---------|-------------|----------|-------|\n")

		for _, m := range metas {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
				m.Display, m.Description, m.Fidelity.Label(), m.State.Label())
		}

		sections = append(sections, strings.TrimRight(b.String(), "\n"))
	}

	return strings.Join(sections, "\n\n"), len(services), nil
}

func apply(content, body string, count int) (string, error) {
	loc := beginMarkerRe.FindStringIndex(content)
	if loc == nil {
		return "", fmt.Errorf("BEGIN SERVICES marker not found")
	}

	endIdx := strings.Index(content, endMarker)
	if endIdx < 0 || endIdx < loc[1] {
		return "", fmt.Errorf("END SERVICES marker not found after BEGIN marker")
	}

	var out strings.Builder

	out.WriteString(content[:loc[1]])
	out.WriteString("\n")
	out.WriteString(body)
	out.WriteString("\n")
	out.WriteString(content[endIdx:])

	return countRe.ReplaceAllString(out.String(), fmt.Sprintf("## Supported Services (%d services)", count)), nil
}
