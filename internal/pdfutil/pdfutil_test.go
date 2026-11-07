package pdfutil

import (
	"slices"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	inputs := []string{"page_001.pdf", "page_002.pdf", "page_003.pdf"}
	output := "scan.pdf"

	args := BuildArgs(inputs, output)

	want := []string{"page_001.pdf", "page_002.pdf", "page_003.pdf", "scan.pdf"}
	if len(args) != len(want) {
		t.Fatalf("args length = %d, want %d; got %v", len(args), len(want), args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, args[i], w)
		}
	}
}

func TestBuildArgsSinglePage(t *testing.T) {
	args := BuildArgs([]string{"only.pdf"}, "out.pdf")
	want := []string{"only.pdf", "out.pdf"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestBuildArgsDoesNotMutateInput(t *testing.T) {
	inputs := []string{"a.pdf", "b.pdf"}
	_ = BuildArgs(inputs, "out.pdf")

	// original slice should still have 2 elements (not 3)
	if len(inputs) != 2 {
		t.Errorf("BuildArgs mutated the input slice: len = %d, want 2", len(inputs))
	}
}

func TestCombinePDFsEmptyInput(t *testing.T) {
	err := CombinePDFs(nil, "out.pdf")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestCombinePDFsEmptyOutput(t *testing.T) {
	err := CombinePDFs([]string{"a.pdf"}, "")
	if err == nil {
		t.Fatal("expected error for empty output path")
	}
}
