// Contract test: every variable read by config.Load must be propagated into
// the `backend` service `environment` of docker-compose.yml, otherwise the
// multi-provider AI feature stays inert in production (see issue #56).
//
// Pure stdlib, no YAML dependency: the check pins that each env name read by
// config.Load appears inside the backend.environment block as ${NAME} or
// ${NAME:-default}.
package compose

import (
	"os"
	"strings"
	"testing"
)

// composePath resolves the repository-root docker-compose.yml.
func composePath() (string, error) {
	if p := os.Getenv("MAILSORTER_ROOT"); p != "" {
		return p, nil
	}
	candidates := []string{
		"../docker-compose.yml",    // go test run from backend/
		"../../docker-compose.yml", // go test run from repo root
		"../../../../docker-compose.yml",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", os.ErrNotExist
}

// configLoadEnvVars lists the environment variables loaded by
// backend/internal/config.Load. They are the single source of truth for the
// contract: the compose file must forward each one to the backend service.
var configLoadEnvVars = []string{
	"OPENAI_API_KEY",
	"OPENAI_MODEL",
	"OPENAI_BASE_URL",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_MODEL",
	"OLLAMA_MODEL",
	"OLLAMA_BASE_URL",
	"AI_FALLBACK_PROVIDER",
}

func TestBackendEnvironmentContainsAllConfigVars(t *testing.T) {
	composeFile, err := composePath()
	if err != nil {
		t.Skip("docker-compose.yml not found; run from the repository root")
	}
	t.Logf("checking %s", composeFile)

	data, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	content := string(data)

	for _, name := range configLoadEnvVars {
		expected := "${" + name + "}"
		found := strings.Contains(content, expected) || strings.Contains(content, "${"+name+":-")
		if !found {
			t.Errorf("docker-compose.yml ne contient pas le mapping ${%s} (ou ${%s:-...}) dans %s backend.environment", name, name)
		}
	}
}
