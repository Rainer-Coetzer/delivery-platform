package packs

import (
	"fmt"
	"slices"
	"strings"
)

// Settings are the numbers a pack's capability modules run on: limits,
// lead times, attempts, sizes. They live in pack.yaml, never in code, so a
// buyer changes a limit by editing their pack (docs/CONVENTIONS.md: no hardcoded
// business constants). A module that needs settings refuses to boot
// without them rather than falling back to a number nobody chose.
type Settings struct {
	Packages   *PackagesSettings   `yaml:"packages"`
	MultiStop  *MultiStopSettings  `yaml:"multi-stop"`
	Scheduling *SchedulingSettings `yaml:"scheduling"`
	Proof      *ProofSettings      `yaml:"proof"`
	Documents  *DocumentsSettings  `yaml:"documents"`
}

// PackagesSettings bound what a customer may declare per package line.
type PackagesSettings struct {
	MaxLines              int   `yaml:"max_lines" json:"maxLines"`
	MaxQuantity           int   `yaml:"max_quantity" json:"maxQuantity"`
	MaxWeightGrams        int64 `yaml:"max_weight_g" json:"maxWeightGrams"`     // per package
	MaxDimensionMm        int   `yaml:"max_dimension_mm" json:"maxDimensionMm"` // longest side
	MaxDeclaredValueCents int64 `yaml:"max_declared_value_cents" json:"maxDeclaredValueCents"`
}

type MultiStopSettings struct {
	MaxStops int `yaml:"max_stops" json:"maxStops"`
}

// SchedulingSettings: how far ahead a customer may book, how narrow a
// window may be, and how long before a window opens dispatch starts.
type SchedulingSettings struct {
	MaxDaysAhead     int `yaml:"max_days_ahead" json:"maxDaysAhead"`
	MinWindowMinutes int `yaml:"min_window_minutes" json:"minWindowMinutes"`
	LeadMinutes      int `yaml:"lead_minutes" json:"leadMinutes"`
}

type ProofSettings struct {
	PinAttempts   int   `yaml:"pin_attempts" json:"pinAttempts"` // wrong PINs before the stop locks
	MaxPhotoBytes int64 `yaml:"max_photo_bytes" json:"maxPhotoBytes"`
}

type DocumentsSettings struct {
	MaxBytes     int64    `yaml:"max_bytes" json:"maxBytes"`
	AllowedTypes []string `yaml:"allowed_types" json:"allowedTypes"` // MIME types, e.g. application/pdf
	MaxPerJob    int      `yaml:"max_per_job" json:"maxPerJob"`
}

// Stop kinds a job may have. A food or courier job uses pickup and dropoff;
// multi-stop adds the rest.
var StopKinds = []string{"pickup", "dropoff", "waypoint", "service", "return"}

// ProofKinds a stop may require.
var ProofKinds = []string{"photo", "signature", "pin", "id_check"}

func (p *Pack) validateSettings(add func(string, ...any)) {
	s := p.Settings
	check := func(module string, set bool) bool {
		switch {
		case set && !p.Has(module):
			add("settings.%s is set but the %s module isn't enabled", module, module)
		case !set && p.Has(module):
			add("the %s module needs settings.%s", module, module)
		}
		return set
	}
	positive := func(where string, v int64) {
		if v <= 0 {
			add("settings.%s must be > 0, got %d", where, v)
		}
	}
	if check(ModPackages, s.Packages != nil) {
		positive("packages.max_lines", int64(s.Packages.MaxLines))
		positive("packages.max_quantity", int64(s.Packages.MaxQuantity))
		positive("packages.max_weight_g", s.Packages.MaxWeightGrams)
		positive("packages.max_dimension_mm", int64(s.Packages.MaxDimensionMm))
		positive("packages.max_declared_value_cents", s.Packages.MaxDeclaredValueCents)
	}
	if check(ModMultiStop, s.MultiStop != nil) && s.MultiStop.MaxStops < 2 {
		add("settings.multi-stop.max_stops must be >= 2, got %d", s.MultiStop.MaxStops)
	}
	if check(ModScheduling, s.Scheduling != nil) {
		positive("scheduling.max_days_ahead", int64(s.Scheduling.MaxDaysAhead))
		positive("scheduling.min_window_minutes", int64(s.Scheduling.MinWindowMinutes))
		if s.Scheduling.LeadMinutes < 0 {
			add("settings.scheduling.lead_minutes must be >= 0, got %d", s.Scheduling.LeadMinutes)
		}
	}
	if check(ModProof, s.Proof != nil) {
		positive("proof.pin_attempts", int64(s.Proof.PinAttempts))
		positive("proof.max_photo_bytes", s.Proof.MaxPhotoBytes)
	}
	if check(ModDocuments, s.Documents != nil) {
		positive("documents.max_bytes", s.Documents.MaxBytes)
		positive("documents.max_per_job", int64(s.Documents.MaxPerJob))
		if len(s.Documents.AllowedTypes) == 0 {
			add("settings.documents.allowed_types needs at least one MIME type")
		}
	}
	for kind, proof := range p.Job.Proof {
		if !slices.Contains(StopKinds, kind) {
			add("job.proof: %q is not a stop kind (valid: %v)", kind, StopKinds)
		}
		if !slices.Contains(ProofKinds, proof) {
			add("job.proof.%s: %q (valid: %v)", kind, proof, ProofKinds)
		}
	}
}

// validateFacts ties the approval module to role-set facts: a lifecycle
// fact that a person sets (`by:`) needs the approval module's endpoint,
// and the module is pointless without one.
func (p *Pack) validateFacts() error {
	roleSet := false
	for _, f := range p.Lifecycle.Facts {
		if len(f.By) > 0 {
			roleSet = true
		}
	}
	switch {
	case roleSet && !p.Has(ModApproval):
		return fmt.Errorf("lifecycle.yaml has facts set by a role, which needs the approval module")
	case !roleSet && p.Has(ModApproval):
		return fmt.Errorf("the approval module is enabled but lifecycle.yaml has no fact set by a role (`by:`)")
	}
	return nil
}

// Revalidate checks the pack's module settings again, after a brand
// overrode some of them (pkg/brand): an override must meet the same rules
// as the pack's own numbers.
func (p *Pack) Revalidate() error {
	var problems []string
	p.validateSettings(func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) })
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}
