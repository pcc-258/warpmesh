package version

import (
	"strings"
	"testing"
)

func TestCurrentHasNonEmptyBuildIdentity(t *testing.T) {
	info := Current()
	if strings.TrimSpace(info.Version) == "" || strings.TrimSpace(info.Commit) == "" || strings.TrimSpace(info.BuildTime) == "" {
		t.Fatalf("incomplete build identity: %+v", info)
	}
}
