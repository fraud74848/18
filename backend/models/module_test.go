// Consolidated source; original file boundaries are marked below.
package models

import (
	testing "testing"
)

// ---- Source: backend/models/policy_config_test.go ----
func TestParseRemoteViewFromPolicyContent(t *testing.T) {
	defaults := ParseRemoteViewFromPolicyContent("")
	if defaults.Width != DefaultRemoteViewWidth || defaults.Height != DefaultRemoteViewHeight || defaults.Fps != DefaultRemoteViewFps {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}

	content := `{
		"version": 1,
		"config": {
			"remoteView": { "width": 1920, "height": 1080, "fps": 15 }
		}
	}`
	parsed := ParseRemoteViewFromPolicyContent(content)
	if parsed.Width != 1920 || parsed.Height != 1080 || parsed.Fps != 15 {
		t.Fatalf("unexpected parsed values: %+v", parsed)
	}
}
func TestParseRemoteViewFromLegacyTopLevelIgnored(t *testing.T) {
	content := `{
		"version": 1,
		"remoteView": { "width": 640, "height": 480, "fps": 5 },
		"config": {}
	}`
	parsed := ParseRemoteViewFromPolicyContent(content)
	if parsed.Width != DefaultRemoteViewWidth {
		t.Fatalf("top-level remoteView must be ignored, got %+v", parsed)
	}
}
