package postprocess

import (
	"image"
	"image/color"
	"testing"
)

func scanImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 7, 255})
		}
	}
	return img
}

func TestCropNominalA4(t *testing.T) {
	p, err := New(Default())
	if err != nil {
		t.Fatal(err)
	}
	src := scanImage(2688, 3507) // full-width V370 scan, A4 at 300 dpi
	out, recs, err := p.Run(src, Context{DPI: 300, AreaMM: [2]float64{210, 297}})
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() != 2480 || b.Dy() != 3508 {
		t.Fatalf("A4 at 300 dpi: %v, want 2480 × 3508", b)
	}
	// Pixels are copied unchanged from (0, 0); the missing row is white.
	if out.RGBAAt(100, 200) != src.RGBAAt(100, 200) || out.RGBAAt(2479, 3506) != src.RGBAAt(2479, 3506) {
		t.Fatal("pixels changed")
	}
	if c := out.RGBAAt(5, 3507); c != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("padding %v, want white", c)
	}
	r := recs[0]
	if r["step"] != StepCrop || r["mode"] != CropNominal || r["padded_px"].(map[string]int)["bottom"] != 1 {
		t.Fatalf("record %v", r)
	}
}

func TestCropSizes(t *testing.T) {
	c, _ := newCrop(StepConfig{Step: StepCrop})
	for _, tc := range []struct {
		dpi   int
		mm    [2]float64
		srcW  int
		srcH  int
		wantW int
		wantH int
	}{
		{1200, [2]float64{148, 210}, 7056, 9921, 6992, 9921}, // A5 at 1200 dpi
		{600, [2]float64{210, 297}, 5008, 7016, 4961, 7016},  // A4 at 600 dpi
		{300, [2]float64{100, 50}, 1536, 591, 1181, 591},
	} {
		out, _, err := c.Apply(scanImage(tc.srcW, tc.srcH), Context{DPI: tc.dpi, AreaMM: tc.mm})
		if err != nil {
			t.Fatal(err)
		}
		if b := out.Bounds(); b.Dx() != tc.wantW || b.Dy() != tc.wantH {
			t.Errorf("%v mm at %d dpi: %v, want %d × %d", tc.mm, tc.dpi, b, tc.wantW, tc.wantH)
		}
	}
	// Whole glass: nothing to crop to.
	src := scanImage(30, 20)
	out, rec, err := c.Apply(src, Context{DPI: 300})
	if err != nil || out != src || rec["skipped"] == nil {
		t.Fatalf("max area: %v %v", rec, err)
	}
}

func TestPipelineConfig(t *testing.T) {
	p, err := New(AsScanned())
	if err != nil || !p.Empty() {
		t.Fatalf("as scanned: %v %v", p, err)
	}
	var nilP *Pipeline
	if !nilP.Empty() || nilP.Names() != nil {
		t.Fatal("nil pipeline")
	}
	for _, c := range []Config{
		{Steps: []StepConfig{{Step: "sharpen"}}},
		{Steps: []StepConfig{{}}},
		{Steps: []StepConfig{{Step: StepCrop, Mode: "true_size"}}},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	p, _ = New(Config{KeepOriginal: true, Steps: []StepConfig{{Step: StepCrop}}})
	if !p.KeepOriginal || len(p.Names()) != 1 {
		t.Fatalf("%+v", p)
	}
	if _, _, err := p.Run(scanImage(4, 4), Context{}); err == nil {
		t.Fatal("missing dpi accepted")
	}
}
