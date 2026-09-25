package packs

import (
	"sort"
	"strings"
)

// PublicConfig is what a client needs to draw this platform without
// hardcoding a business: the pack's words, its lifecycle (states, their
// labels and milestones), what a job looks like, and the limits a form
// should enforce before the server does. Served by GET /config, before
// login, so nothing in it may be secret.
type PublicConfig struct {
	Pack           PackInfo          `json:"pack"`
	Locale         string            `json:"locale"`
	Modules        []string          `json:"modules"`
	Roles          []string          `json:"roles"`
	Job            JobInfo           `json:"job"`
	PaymentMethods []string          `json:"paymentMethods"`
	Lifecycle      LifecycleInfo     `json:"lifecycle"`
	Terms          map[string]string `json:"terms"`
	Limits         Limits            `json:"limits"`
}

type PackInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type JobInfo struct {
	Stops string            `json:"stops"`
	Lines string            `json:"lines"`
	Proof map[string]string `json:"proof,omitempty"`
}

type LifecycleInfo struct {
	Initial string      `json:"initial"`
	States  []StateInfo `json:"states"`
	// Facts people set (approval module), and which roles may.
	Approvals []ApprovalInfo `json:"approvals,omitempty"`
}

type StateInfo struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Milestone string `json:"milestone,omitempty"`
	Terminal  bool   `json:"terminal,omitempty"`
}

type ApprovalInfo struct {
	Fact  string   `json:"fact"`
	Label string   `json:"label"`
	By    []string `json:"by"`
}

// Limits are the module settings a form needs; absent when the module is
// off.
type Limits struct {
	Packages   *PackagesSettings   `json:"packages,omitempty"`
	MultiStop  *MultiStopSettings  `json:"multiStop,omitempty"`
	Scheduling *SchedulingSettings `json:"scheduling,omitempty"`
	Documents  *DocumentsLimits    `json:"documents,omitempty"`
	Proof      *ProofLimits        `json:"proof,omitempty"`
}

type DocumentsLimits struct {
	MaxBytes     int64    `json:"maxBytes"`
	AllowedTypes []string `json:"allowedTypes"`
	MaxPerJob    int      `json:"maxPerJob"`
}

// ProofLimits leaves out pin_attempts: telling a guesser how many tries
// they get helps nobody else.
type ProofLimits struct {
	MaxPhotoBytes int64 `json:"maxPhotoBytes"`
}

// Config builds the public configuration in locale (falling back to English
// for any word the pack doesn't translate).
func (p *Pack) Config(locale string, paymentMethods []string) PublicConfig {
	if _, ok := p.Terminology[locale]; !ok {
		locale = "en"
	}
	terms := map[string]string{}
	keys := map[string]bool{}
	for k := range p.Terminology["en"] {
		keys[k] = true
	}
	for k := range p.Terminology[locale] {
		keys[k] = true
	}
	for k := range keys {
		terms[k] = p.Term(locale, k)
	}

	var states []StateInfo
	for _, s := range p.Lifecycle.States {
		states = append(states, StateInfo{Name: s.Name, Label: p.label(locale, "state."+s.Name, s.Name),
			Milestone: s.Milestone, Terminal: s.Terminal})
	}
	var approvals []ApprovalInfo
	for _, f := range p.Lifecycle.Facts {
		if len(f.By) > 0 {
			approvals = append(approvals, ApprovalInfo{Fact: f.Name, Label: p.label(locale, "fact."+f.Name, f.Name), By: f.By})
		}
	}

	methods := append([]string(nil), paymentMethods...)
	sort.Strings(methods)
	cfg := PublicConfig{
		Pack:           PackInfo{Name: p.Name, Description: p.Description},
		Locale:         locale,
		Modules:        p.Modules,
		Roles:          p.Roles,
		Job:            JobInfo{Stops: p.Job.Stops, Lines: p.Job.Lines, Proof: p.Job.Proof},
		PaymentMethods: methods,
		Lifecycle:      LifecycleInfo{Initial: p.Lifecycle.Initial, States: states, Approvals: approvals},
		Terms:          terms,
		Limits: Limits{
			Packages:   p.Settings.Packages,
			MultiStop:  p.Settings.MultiStop,
			Scheduling: p.Settings.Scheduling,
		},
	}
	if d := p.Settings.Documents; d != nil {
		cfg.Limits.Documents = &DocumentsLimits{MaxBytes: d.MaxBytes, AllowedTypes: d.AllowedTypes, MaxPerJob: d.MaxPerJob}
	}
	if pr := p.Settings.Proof; pr != nil {
		cfg.Limits.Proof = &ProofLimits{MaxPhotoBytes: pr.MaxPhotoBytes}
	}
	return cfg
}

// label is the pack's word for key, or a readable form of the name when
// the pack gives none (IN_TRANSIT -> "In transit").
func (p *Pack) label(locale, key, name string) string {
	if w := p.Term(locale, key); w != key {
		return w
	}
	words := strings.ToLower(strings.ReplaceAll(name, "_", " "))
	if words == "" {
		return name
	}
	return strings.ToUpper(words[:1]) + words[1:]
}
