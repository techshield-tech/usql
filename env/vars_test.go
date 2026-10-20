package env

import (
	"os"
	"testing"
)

// TestSetConnAllowsHyphen verifies that config-friendly connection names preserve existing profiles.
func TestSetConnAllowsHyphen(t *testing.T) {
	// variables is an isolated connection registry for the compatibility check.
	variables := NewVars()
	// A hyphenated profile name must be accepted without changing ordinary variable rules.
	if err := variables.SetConn("team-service-dev", "dm://example"); err != nil {
		t.Fatalf("SetConn() unexpected error: %v", err)
	}
	// The original profile name must remain available for command-line lookup.
	if _, ok := variables.GetConn("team-service-dev"); !ok {
		t.Fatal("GetConn() = false, want true")
	}
}

// The connector sets PAGE to an empty value to turn the pager off, even when
// the host sets PAGER.
func TestPageTurnsPagerOff(t *testing.T) {
	t.Setenv("USQL_PAGER", "")
	if err := os.Unsetenv("USQL_PAGER"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAGER", "more")
	t.Setenv("PAGE", "")
	if got := NewDefaultVars().Print()["pager"]; got != "off" {
		t.Errorf("pager = %q, want off", got)
	}
}
