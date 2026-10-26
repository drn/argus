package agent

import (
	"fmt"
	"strconv"
	"strings"
)

// agentGitPolicy appends command-scope Git config to the child environment.
// Existing entries retain order and contents. A legacy GIT_CONFIG_PARAMETERS
// value is parsed by Git after COUNT, so append there too when it is present.
// Neither command-line flags (-S / -c) nor repository files are changed.
func agentGitPolicy(env []string, policy string) ([]string, error) {
	if policy != "unsigned" {
		return env, nil
	}
	var count, parameters string
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "GIT_CONFIG_COUNT":
			count = value
		case "GIT_CONFIG_PARAMETERS":
			parameters = value
		}
	}
	n := 0
	if count != "" {
		parsed, err := strconv.Atoi(count)
		if err != nil || parsed < 0 || parsed == int(^uint(0)>>1) {
			return nil, fmt.Errorf("agent Git policy: invalid GIT_CONFIG_COUNT")
		}
		n = parsed
	}
	out := append([]string(nil), env...)
	out = append(out,
		"GIT_CONFIG_COUNT="+strconv.Itoa(n+1),
		"GIT_CONFIG_KEY_"+strconv.Itoa(n)+"=commit.gpgsign",
		"GIT_CONFIG_VALUE_"+strconv.Itoa(n)+"=false",
	)
	if parameters != "" {
		out = append(out, "GIT_CONFIG_PARAMETERS="+parameters+" 'commit.gpgsign=false'")
	}
	return out, nil
}
