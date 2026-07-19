package media

import (
	"reflect"
	"testing"
)

func TestBuildVipsArrayJoinArgs(t *testing.T) {
	args, err := BuildVipsArrayJoinArgs(VipsJoinSpec{Inputs: []string{"1.png", "2.png"}, Output: "sheet.avif", Columns: 10, Quality: 75, Effort: 5})
	if err != nil {
		t.Fatalf("BuildVipsArrayJoinArgs: %v", err)
	}
	want := []string{"arrayjoin", "1.png 2.png", "sheet.avif[Q=75,effort=5]", "--across", "10", "--vips-progress"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildVipsArrayJoinArgsRejectsInvalidSettings(t *testing.T) {
	if _, err := BuildVipsArrayJoinArgs(VipsJoinSpec{Inputs: []string{"1.png"}, Output: "sheet.avif", Columns: 0}); err == nil {
		t.Fatalf("expected missing columns to fail")
	}
	if _, err := BuildVipsArrayJoinArgs(VipsJoinSpec{Inputs: []string{"1.png"}, Output: "sheet.avif", Columns: 1, Quality: 101}); err == nil {
		t.Fatalf("expected invalid quality to fail")
	}
}

func TestBuildVipsResizeArgs(t *testing.T) {
	args, err := BuildVipsResizeArgs(VipsResizeSpec{Input: "master.avif", Output: "small.avif", Scale: 0.5, Quality: 60, Effort: 2})
	if err != nil {
		t.Fatalf("BuildVipsResizeArgs: %v", err)
	}
	want := []string{"resize", "master.avif", "small.avif[Q=60,effort=2]", "0.5", "--vips-progress"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
}
