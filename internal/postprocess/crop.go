package postprocess

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
)

// StepCrop cuts the scan to the requested size.
const StepCrop = "crop"

// Crop modes.
const (
	// CropNominal: exactly the requested size at the nominal resolution
	// (A4 at 300 dpi → 2480 × 3508 px), starting at the first scanned pixel
	// (the corner side of the glass). Pixels are copied unchanged; where the
	// scan is smaller, the rest is filled with white.
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

// px converts mm at dpi to whole pixels (rounded).
func px(mm float64, dpi int) int { return int(math.Round(mm * float64(dpi) / 25.4)) }

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
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), img, src.Min, draw.Src)
	rec := Record{
		"mode":      c.mode,
		"box_px":    []int{0, 0, w, h},
		"source_px": []int{src.Dx(), src.Dy()},
		"area_mm":   []float64{ctx.AreaMM[0], ctx.AreaMM[1]},
		"dpi":       ctx.DPI,
	}
	if pr, pb := max(0, w-src.Dx()), max(0, h-src.Dy()); pr > 0 || pb > 0 {
		rec["padded_px"] = map[string]int{"right": pr, "bottom": pb}
	}
	return out, rec, nil
}
