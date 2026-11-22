package api

import (
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

// Every edition x plan combination: only a free account of the hosted service
// is held to the monthly cap.
func TestAnalysisCapFor(t *testing.T) {
	cases := []struct {
		edition provider.Edition
		plan    string
		want    int
	}{
		{provider.EditionHosted, PlanFree, FreeMonthlyLimit},
		{provider.EditionHosted, PlanPro, -1},
		{provider.EditionSelfHosted, PlanFree, -1},
		{provider.EditionSelfHosted, PlanPro, -1},
		{provider.EditionHosted, "", FreeMonthlyLimit},
		{provider.EditionSelfHosted, "", -1},
	}
	for _, c := range cases {
		if got := analysisCapFor(c.edition, c.plan); got != c.want {
			t.Errorf("analysisCapFor(%q, %q) = %d, want %d", c.edition, c.plan, got, c.want)
		}
	}
}
