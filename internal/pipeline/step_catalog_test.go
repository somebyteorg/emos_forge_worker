package pipeline

import "testing"

func TestStepCatalogNames(t *testing.T) {
	if got := ExternalStepName(StepSubtitlePackage); got != "subtitle_package" {
		t.Fatalf("external name = %q", got)
	}
	if got := ExternalStepName("custom.step"); got != "custom_step" {
		t.Fatalf("fallback external name = %q", got)
	}
}
