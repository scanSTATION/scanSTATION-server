// Package postprocess runs the server's postprocessing steps on a scanned
// image before it is stored: a pipeline of independent steps (crop today;
// later e.g. document detection, deskew, background, OCR). Each step
// returns a record that goes into the scan's JSON sidecar, so every change
// to the scan is traceable.
//
// Steps are configured by name (Config); New builds the pipeline and
// rejects unknown steps or options, so a typo in the configuration fails at
// start-up instead of silently scanning differently.
package postprocess

import (
	"errors"
	"fmt"
	"image"
)

// Context describes the scan a pipeline runs on.
type Context struct {
	DPI    int        // nominal resolution of the image
	AreaMM [2]float64 // requested width and height in mm
}

// Record documents one step's result (JSON in the sidecar).
type Record map[string]any

// Step is one postprocessing step.
type Step interface {
	Name() string
	Apply(img *image.RGBA, c Context) (*image.RGBA, Record, error)
}

// StepConfig configures one step: "step" names it, the other fields are
// the step's options.
type StepConfig struct {
	Step string `json:"step"`
	Mode string `json:"mode,omitempty"` // crop: "nominal" (default)
}

// Config is the postprocessing section of the configuration.
type Config struct {
	// KeepOriginal additionally stores the unprocessed scan
	// (<name>-original.<ext>).
	KeepOriginal bool `json:"keep_original,omitempty"`
	// Steps run in order; empty keeps the scan exactly as scanned.
	Steps []StepConfig `json:"steps"`
}

// Default crops to exactly the requested size.
func Default() Config {
	return Config{Steps: []StepConfig{{Step: StepCrop, Mode: CropNominal}}}
}

// AsScanned keeps the scan exactly as scanned.
func AsScanned() Config { return Config{Steps: []StepConfig{}} }

// Pipeline is a validated list of steps.
type Pipeline struct {
	Steps        []Step
	KeepOriginal bool
}

// New validates c and builds its pipeline.
func New(c Config) (*Pipeline, error) {
	p := &Pipeline{KeepOriginal: c.KeepOriginal}
	for i, sc := range c.Steps {
		st, err := newStep(sc)
		if err != nil {
			return nil, fmt.Errorf("postprocessing step %d: %w", i+1, err)
		}
		p.Steps = append(p.Steps, st)
	}
	return p, nil
}

func newStep(sc StepConfig) (Step, error) {
	switch sc.Step {
	case StepCrop:
		return newCrop(sc)
	case "":
		return nil, errors.New(`missing "step"`)
	}
	return nil, fmt.Errorf("unknown step %q (available: %s)", sc.Step, StepCrop)
}

// Empty reports whether the pipeline leaves the scan as scanned.
func (p *Pipeline) Empty() bool { return p == nil || len(p.Steps) == 0 }

// Names lists the steps.
func (p *Pipeline) Names() []string {
	var n []string
	if p != nil {
		for _, s := range p.Steps {
			n = append(n, s.Name())
		}
	}
	return n
}

// Run applies the steps in order and returns the result and one record per
// step.
func (p *Pipeline) Run(img *image.RGBA, c Context) (*image.RGBA, []Record, error) {
	if c.DPI <= 0 {
		return nil, nil, fmt.Errorf("postprocessing: invalid resolution %d", c.DPI)
	}
	var recs []Record
	for _, s := range p.Steps {
		out, rec, err := s.Apply(img, c)
		if err != nil {
			return nil, nil, fmt.Errorf("postprocessing %s: %w", s.Name(), err)
		}
		if rec == nil {
			rec = Record{}
		}
		rec["step"] = s.Name()
		recs = append(recs, rec)
		img = out
	}
	return img, recs, nil
}
