package amarit

import (
	"encoding/base64"
	"encoding/json"
	"os"
)

// handoffValuesEnv carries the values as base64url JSON across the exec.
const handoffValuesEnv = "AMARIT_HANDOFF_VALUES"

func prepareValues(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return handoffValuesEnv + "=" + base64.RawURLEncoding.EncodeToString(data)
}

// InheritedValues returns the values the previous process handed over
// through the restart with HandoffValues: a child's pid, a port, a token.
// Empty on a fresh start. The variable is cleared so programs this process
// starts never see it.
func InheritedValues() map[string]string {
	spec := os.Getenv(handoffValuesEnv)
	if spec == "" {
		return nil
	}
	os.Unsetenv(handoffValuesEnv)
	data, err := base64.RawURLEncoding.DecodeString(spec)
	if err != nil {
		return nil
	}
	var values map[string]string
	if json.Unmarshal(data, &values) != nil {
		return nil
	}
	return values
}
