package postprocess

import (
	"fmt"
	"image"
	"image/draw"
	"math"
)

// StepCrop cuts the scan to the requested size.
const StepCrop = "crop"

// Crop modes.
const (
	// CropNominal: the requested size at the nominal resolution, rounded
	// down like other scanning software (A4 at 300 dpi → 2480 × 3507 px),
	// starting at the first scanned pixel (the glass corner). Pixels are
	// copied unchanged and nothing is added: a scan smaller than the
	// requested size stays smaller.
	CropNominal = "nominal"
)

type crop struct{ mode string }

func newCrop(sc StepConfig) (Step, error) {
	switch sc.Mode {
	case "", CropNominal:
		return crop{mode: CropNominal}, nil
	}
	return nil, fmt.Errorf("crop: unknown mode %q (available: %s)", sc.Mode, CropNominal)
}

func (crop) Name() string { return StepCrop }

// px converts mm at dpi to whole pixels, rounded down (a tiny tolerance
// keeps exact values such as 1181.0000001 from losing a pixel).
func px(mm float64, dpi int) int { return int(math.Floor(mm*float64(dpi)/25.4 + 1e-6)) }

func (c crop) Apply(img *image.RGBA, ctx Context) (*image.RGBA, Record, error) {
	if ctx.AreaMM[0] == 0 && ctx.AreaMM[1] == 0 {
		// Whole glass requested: nothing to crop to.
		return img, Record{"mode": c.mode, "skipped": "no requested size (whole glass)"}, nil
	}
	w, h := px(ctx.AreaMM[0], ctx.DPI), px(ctx.AreaMM[1], ctx.DPI)
	if w <= 0 || h <= 0 {
		return nil, nil, fmt.Errorf("requested area %.1f × %.1f mm is empty", ctx.AreaMM[0], ctx.AreaMM[1])
	}
	src := img.Bounds()
	cw, ch := min(w, src.Dx()), min(h, src.Dy())
	out := image.NewRGBA(image.Rect(0, 0, cw, ch))
	draw.Draw(out, out.Bounds(), img, src.Min, draw.Src)
	rec := Record{
		"mode":      c.mode,
		"box_px":    []int{0, 0, cw, ch},
		"source_px": []int{src.Dx(), src.Dy()},
		"area_mm":   []float64{ctx.AreaMM[0], ctx.AreaMM[1]},
		"dpi":       ctx.DPI,
	}
	if cw < w || ch < h {
		rec["requested_px"] = []int{w, h} // the scan was smaller; nothing added
	}
	return out, rec, nil
}
