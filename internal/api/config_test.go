package api

import (
	"slices"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/config"
)

// TestConfigEnvNamesMatchLoader: every environment override of an admin
// setting is collected by config.Load, and Load collects nothing else.
func TestConfigEnvNamesMatchLoader(t *testing.T) {
	got, want := ConfigEnvNames(), slices.Clone(config.OverrideVars)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("api config env names %v, config.OverrideVars %v", got, want)
	}
}
