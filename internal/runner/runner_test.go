package runner

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestScanReportsCarriageReturnProgressLines(t *testing.T) {
	var captured bytes.Buffer
	var lines []string
	done := make(chan struct{}, 1)
	scan(strings.NewReader("10%\r20%\nend"), "stderr", &captured, func(_ string, line string) {
		lines = append(lines, line)
	}, done)
	<-done
	if !reflect.DeepEqual(lines, []string{"10%", "20%", "end"}) {
		t.Fatalf("lines = %#v", lines)
	}
	if captured.String() != "10%\r20%\nend" {
		t.Fatalf("captured = %q", captured.String())
	}
}
