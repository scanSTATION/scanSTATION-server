package scan

import "testing"

func TestValidatePrefix(t *testing.T) {
	for _, ok := range []string{"scan", "scan_2026-11-07", "Rechnung 12"} {
		if err := ValidatePrefix(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".hidden", "..", "a/b", `a\b`, "a*", "a?", "a[1]", "a\x00b", string(make([]byte, MaxPrefixLen+1))} {
		if err := ValidatePrefix(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(Default()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ScanRequest){
		"resolution": func(r *ScanRequest) { r.Resolution = 10 },
		"quality":    func(r *ScanRequest) { r.JpegQuality = 0 },
		"area":       func(r *ScanRequest) { r.Size, r.WidthMM, r.HeightMM = SizeUserDef, 0, 100 },
		"huge area":  func(r *ScanRequest) { r.Size, r.WidthMM, r.HeightMM = SizeUserDef, 600, 100 },
		"format":     func(r *ScanRequest) { r.Format = 42 },
		"size":       func(r *ScanRequest) { r.Size = 99 },
		"prefix":     func(r *ScanRequest) { r.FileNamePrefix = "../x" },
	}
	for name, mod := range cases {
		r := Default()
		mod(&r)
		if Validate(r) == nil {
			t.Errorf("%s: invalid request accepted", name)
		}
	}
	r := Default()
	r.Size = SizeMaxArea
	if err := Validate(r); err != nil {
		t.Errorf("max area: %v", err)
	}
}

func TestAreaMM(t *testing.T) {
	r := Default()
	if w, h, _ := r.AreaMM(); w != 210 || h != 297 {
		t.Fatalf("A4 = %v × %v", w, h)
	}
	r.Size, r.WidthMM, r.HeightMM = SizeUserDef, 148, 210
	if w, h, _ := r.AreaMM(); w != 148 || h != 210 {
		t.Fatalf("user = %v × %v", w, h)
	}
}
