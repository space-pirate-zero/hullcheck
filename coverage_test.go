package hullcheck_test

import (
	"testing"

	"github.com/spaceship-alpha-9/hullcheck"
)

// This is the integration this project recommends, applied to itself. If adding
// hullcheck to a Go test suite is not this small, the README is lying.
func TestOwnGateCoverage(t *testing.T) {
	rep := hullcheck.AssertCoverage(t, ".", 80)
	t.Logf("gate coverage %.0f%% across %d rules in %v",
		rep.Coverage(), len(rep.Findings), rep.Docs)
}
