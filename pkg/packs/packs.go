// Package packs loads a vertical pack: the config that turns the generic
// core into food delivery, a courier service or a freight board (ADR-0003).
//
// A pack is a directory, packs/<name>/, holding:
//
//	pack.yaml       modules, job shape, strategies, roles, terminology
//	lifecycle.yaml  states, milestones, facts, transitions (pkg/lifecycle)
//
// The shipped packs are embedded in the binary (packs/embed.go). Buyers add
// their own directory next to them (`platformctl new-pack`, ADR-0004), and it
// is embedded the same way at build time. PACKS_DIR can point at a directory
// on disk instead, to iterate on a pack without rebuilding.
package packs

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
)

// SchemaVersion is the pack.yaml format this code reads (ADR-0004: a
// config at another version is rejected with instructions, never misread).
const SchemaVersion = 1

// Capability modules a pack can enable (ADR-0003 section 4).
const (
	ModCatalogue  = "catalogue"
	ModPackages   = "packages"
	ModMultiStop  = "multi-stop"
	ModScheduling = "scheduling"
	ModProof      = "proof"
	ModDocuments  = "documents"
	ModCapacity   = "capacity"
	ModApproval   = "approval"
)

var allModules = []string{ModCatalogue, ModPackages, ModMultiStop, ModScheduling, ModProof, ModDocuments, ModCapacity, ModApproval}

// Stop templates: who a job's stops belong to when a customer creates it.
const (
	StopsMerchantToCustomer = "merchant_to_customer" // pickup at the merchant, dropoff at the customer (food)
	StopsPointToPoint       = "point_to_point"       // customer gives pickup and dropoff (courier)
	StopsMultiStop          = "multi_stop"           // customer gives 2..n stops (freight; needs multi-stop)
)

// What a job's lines are.
const (
	LinesCatalogue = "catalogue" // picked from a merchant's catalogue
	LinesPackages  = "packages"  // freeform: description, weight, dimensions
)

// Strategy is a strategy choice from pack.yaml: a registered name and its
// params, decoded later by that strategy's own factory (ADR-0004).
type Strategy struct {
	Name   string    `yaml:"name"`
	Params yaml.Node `yaml:"params"`
}

type JobShape struct {
	Stops string `yaml:"stops"`
	Lines string `yaml:"lines"`
	// Proof required per stop kind, e.g. {dropoff: photo}. Needs `proof`.
	Proof map[string]string `yaml:"proof"`
}

type Strategies struct {
	Pricing  Strategy   `yaml:"pricing"`
	Dispatch Strategy   `yaml:"dispatch"`
	ETA      Strategy   `yaml:"eta"`
	Payment  []Strategy `yaml:"payment"`
	Notify   []Strategy `yaml:"notify"`
}

// Pack is a loaded, validated vertical pack.
type Pack struct {
	Schema      int                          `yaml:"schema"`
	Name        string                       `yaml:"name"`
	Description string                       `yaml:"description"`
	Modules     []string                     `yaml:"modules"`
	Roles       []string                     `yaml:"roles"`
	Job         JobShape                     `yaml:"job"`
	Strategies  Strategies                   `yaml:"strategies"`
	Terminology map[string]map[string]string `yaml:"terminology"` // locale -> key -> word

	Lifecycle *lifecycle.Machine `yaml:"-"`
}

// Has reports whether the pack enables capability module m.
func (p *Pack) Has(m string) bool { return slices.Contains(p.Modules, m) }

// Term returns the pack's word for key in locale, falling back to English
// and then to key itself, so a missing translation shows up as a readable
// key rather than an empty label.
func (p *Pack) Term(locale, key string) string {
	if w := p.Terminology[locale][key]; w != "" {
		return w
	}
	if w := p.Terminology["en"][key]; w != "" {
		return w
	}
	return key
}

// Load reads pack name from fsys (the embedded packs, or PACKS_DIR) and
// validates it against the events and roles the core knows.
func Load(fsys fs.FS, name string, known lifecycle.Known) (*Pack, error) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return nil, fmt.Errorf("pack name %q: use a plain directory name under packs/", name)
	}
	raw, err := fs.ReadFile(fsys, path.Join(name, "pack.yaml"))
	if err != nil {
		return nil, fmt.Errorf("pack %q: %w (available: %s)", name, err, strings.Join(List(fsys), ", "))
	}
	var p Pack
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("pack %q: pack.yaml: %w", name, err)
	}
	if err := p.validate(name, known); err != nil {
		return nil, err
	}
	lc, err := fs.ReadFile(fsys, path.Join(name, "lifecycle.yaml"))
	if err != nil {
		return nil, fmt.Errorf("pack %q: %w", name, err)
	}
	// Every pack dispatches; a pack without a ready_for_dispatch state
	// would create jobs no agent ever gets.
	p.Lifecycle, err = lifecycle.Load(lc, known, lifecycle.ReadyForDispatch)
	if err != nil {
		return nil, fmt.Errorf("pack %q: lifecycle.yaml: %w", name, err)
	}
	return &p, nil
}

func (p *Pack) validate(dir string, known lifecycle.Known) error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if p.Schema != SchemaVersion {
		add("schema %d is not supported (this build reads schema %d)", p.Schema, SchemaVersion)
	}
	if p.Name != dir {
		add("name %q must match its directory %q", p.Name, dir)
	}
	for _, m := range p.Modules {
		if !slices.Contains(allModules, m) {
			add("unknown module %q (valid: %s)", m, strings.Join(allModules, ", "))
		}
	}
	for _, r := range p.Roles {
		if !slices.Contains(known.Roles, r) {
			add("unknown role %q", r)
		}
	}
	switch p.Job.Stops {
	case StopsMerchantToCustomer, StopsPointToPoint:
	case StopsMultiStop:
		if !p.Has(ModMultiStop) {
			add("job.stops: multi_stop needs the multi-stop module")
		}
	default:
		add("job.stops %q (valid: %s, %s, %s)", p.Job.Stops, StopsMerchantToCustomer, StopsPointToPoint, StopsMultiStop)
	}
	switch p.Job.Lines {
	case LinesCatalogue:
		if !p.Has(ModCatalogue) {
			add("job.lines: catalogue needs the catalogue module")
		}
	case LinesPackages:
		if !p.Has(ModPackages) {
			add("job.lines: packages needs the packages module")
		}
	default:
		add("job.lines %q (valid: %s, %s)", p.Job.Lines, LinesCatalogue, LinesPackages)
	}
	if len(p.Job.Proof) > 0 && !p.Has(ModProof) {
		add("job.proof is set but the proof module isn't enabled")
	}
	for kind, proof := range p.Job.Proof {
		if !slices.Contains([]string{"photo", "signature", "pin", "id_check"}, proof) {
			add("job.proof.%s: %q (valid: photo, signature, pin, id_check)", kind, proof)
		}
	}
	if p.Strategies.Pricing.Name == "" || p.Strategies.Dispatch.Name == "" || p.Strategies.ETA.Name == "" || len(p.Strategies.Payment) == 0 {
		add("strategies: pricing, dispatch, eta and at least one payment are required")
	}
	if _, ok := p.Terminology["en"]; !ok {
		add("terminology: an en locale is required (it's the fallback)")
	}
	if len(problems) > 0 {
		return fmt.Errorf("pack %q is invalid:\n  - %s", dir, strings.Join(problems, "\n  - "))
	}
	return nil
}

// List returns the pack names available in fsys.
func List(fsys fs.FS) []string {
	entries, _ := fs.ReadDir(fsys, ".")
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := fs.Stat(fsys, path.Join(e.Name(), "pack.yaml")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	return out
}
