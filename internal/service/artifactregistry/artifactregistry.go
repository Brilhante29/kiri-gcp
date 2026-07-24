// Package artifactregistry emulates Artifact Registry
// (artifactregistry.googleapis.com/v1): repositories and their packages.
package artifactregistry

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "artifactregistry"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type pkg struct {
	Name string `json:"name"`
}

type repository struct {
	Name        string          `json:"name"`
	Format      string          `json:"format,omitempty"`
	Description string          `json:"description,omitempty"`
	Packages    map[string]*pkg `json:"packages"`
}

type state struct {
	Repositories map[string]*repository `json:"repositories"` // full path -> repo
}

// Service implements the Artifact Registry emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Artifact Registry store.
func New() *Service { return &Service{st: state{Repositories: map[string]*repository{}}} }

func (s *Service) ensureMaps() {
	if s.st.Repositories == nil {
		s.st.Repositories = map[string]*repository{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Artifact Registry",
		Category:    "Containers",
		Description: "Docker, Maven, npm, and apt package repositories",
		Fidelity:    service.FidelityB,
		State:       service.StateBehavioral,
	}
}

// Close persists state if configured.
func (s *Service) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return storage.Save(serviceName, "state", s.st)
}

// RegisterRoutes registers the Artifact Registry REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/repositories"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{repo}", s.get)
	r.Handle("DELETE", base+"/{repo}", s.delete)

	pkgBase := base + "/{repo}/packages"
	r.Handle("GET", pkgBase, s.listPackages)
	r.Handle("GET", pkgBase+"/{pkg}", s.getPackage)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/repositories/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repositoryId")

	var body repository
	_ = httpx.DecodeJSON(r, &body)

	// The real API takes the id via ?repositoryId=; this emulator also
	// accepts (and falls back to) a "name" body field, auto-generating one
	// if neither is present.
	if repoID == "" {
		repoID = body.Name
	}

	if repoID == "" {
		repoID = "repo-" + httpx.ID(4)
	}

	name := s.prefix(r) + repoID
	body.Name = name
	body.Packages = map[string]*pkg{}

	if body.Format == "" {
		body.Format = "DOCKER"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Repositories[name]; exists {
		httpx.AlreadyExists(w, "repository already exists: "+name)

		return
	}

	s.st.Repositories[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Repositories {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*repository, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Repositories[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"repositories": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("repo")

	s.mu.RLock()
	repo, ok := s.st.Repositories[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "repository not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, repo)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("repo")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Repositories[name]; !ok {
		httpx.NotFound(w, "repository not found: "+name)

		return
	}

	delete(s.st.Repositories, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) listPackages(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repo, ok := s.st.Repositories[s.prefix(r)+r.PathValue("repo")]
	if !ok {
		httpx.NotFound(w, "repository not found")

		return
	}

	names := make([]string, 0, len(repo.Packages))
	for n := range repo.Packages {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*pkg, 0, len(names))
	for _, n := range names {
		items = append(items, repo.Packages[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"packages": items})
}

func (s *Service) getPackage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repo, ok := s.st.Repositories[s.prefix(r)+r.PathValue("repo")]
	if !ok {
		httpx.NotFound(w, "repository not found")

		return
	}

	p, ok := repo.Packages[r.PathValue("pkg")]
	if !ok {
		httpx.NotFound(w, "package not found: "+r.PathValue("pkg"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}
