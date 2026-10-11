package amarit

import (
	"strings"
	"testing"
)

func TestHandoffValuesRoundTrip(t *testing.T) {
	entry := prepareValues(map[string]string{"child_pid": "4242", "port": "8090", "note": "has = and , in it"})
	if !strings.HasPrefix(entry, handoffValuesEnv+"=") {
		t.Fatalf("entry %q", entry)
	}
	t.Setenv(handoffValuesEnv, strings.TrimPrefix(entry, handoffValuesEnv+"="))
	got := InheritedValues()
	if got["child_pid"] != "4242" || got["port"] != "8090" || got["note"] != "has = and , in it" {
		t.Fatalf("inherited %v", got)
	}
	if InheritedValues() != nil {
		t.Fatal("the values must be cleared after reading so children do not see them")
	}
	if prepareValues(nil) != "" {
		t.Fatal("nothing to hand over must add nothing to the environment")
	}
}
