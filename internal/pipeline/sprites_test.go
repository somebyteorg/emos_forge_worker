package pipeline

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPadSpriteGridInputsFillsConfiguredGrid(t *testing.T) {
	inputs := []string{"frame_000001.png", "frame_000002.png", "frame_000003.png"}

	got, err := padSpriteGridInputs(inputs, 10, 10, "padding.png")
	if err != nil {
		t.Fatalf("padSpriteGridInputs: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("padded input count = %d, want 100", len(got))
	}
	for index, want := range inputs {
		if got[index] != want {
			t.Fatalf("input %d = %q, want %q", index, got[index], want)
		}
	}
	for index := len(inputs); index < len(got); index++ {
		if got[index] != "padding.png" {
			t.Fatalf("padding input %d = %q, want black padding frame", index, got[index])
		}
	}
}

func TestPadSpriteGridInputsRejectsOverflow(t *testing.T) {
	inputs := []string{"1.png", "2.png", "3.png", "4.png", "5.png"}
	if _, err := padSpriteGridInputs(inputs, 2, 2, "padding.png"); err == nil {
		t.Fatal("expected grid overflow to fail")
	}
}

func TestWriteBlackSpriteFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "padding.png")
	if err := writeBlackSpriteFrame(path, 32, 18); err != nil {
		t.Fatalf("writeBlackSpriteFrame: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open padding frame: %v", err)
	}
	defer file.Close()
	frame, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode padding frame: %v", err)
	}
	if frame.Bounds().Dx() != 32 || frame.Bounds().Dy() != 18 {
		t.Fatalf("padding dimensions = %v, want 32x18", frame.Bounds())
	}
	r, g, b, a := frame.At(0, 0).RGBA()
	if r != 0 || g != 0 || b != 0 || a != 0xffff {
		t.Fatalf("padding pixel = rgba(%d,%d,%d,%d), want opaque black", r, g, b, a)
	}
}

func TestSpriteArtifactSpecUsesFullGridDimensions(t *testing.T) {
	spec := spriteArtifactSpec(
		StepSpritesGenerate,
		spriteSize{Name: "320x180", Width: 320, Height: 180},
		"sprites/320x180/sprite_0001.avif",
		10,
		10,
		0,
		3,
		10,
		[]float64{3.2, 13.1, 23},
		"keyframe_master",
	)

	metadata, ok := spec.Metadata.(spriteMetadata)
	if !ok {
		t.Fatalf("sprite metadata has type %T", spec.Metadata)
	}
	if metadata.Width != 3200 || metadata.Height != 1800 || metadata.Columns != 10 || metadata.Rows != 10 || metadata.GridRows != 10 {
		t.Fatalf("unexpected fixed-grid metadata: %+v", metadata)
	}
	if metadata.FrameCount != 3 || len(metadata.TimestampsSeconds) != 3 {
		t.Fatalf("padding changed effective frames: %+v", metadata)
	}
}
