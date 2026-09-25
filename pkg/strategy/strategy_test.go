package strategy

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type greeter interface{ Greet() string }

type hello struct {
	Name string `yaml:"name"`
}

func (h hello) Greet() string { return "hello " + h.Name }

func node(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}

func newTestRegistry() *Registry[greeter] {
	r := NewRegistry[greeter]("greeting")
	r.Register("hello", func(p Params) (greeter, error) {
		var h hello
		if err := p.Decode(&h); err != nil {
			return nil, err
		}
		if h.Name == "" {
			return nil, errors.New("name is required")
		}
		return h, nil
	})
	r.Register("silent", func(Params) (greeter, error) { return hello{}, nil })
	return r
}

func TestBuild(t *testing.T) {
	r := newTestRegistry()
	g, err := r.Build("hello", NewParams("greeting hello", node(t, "{name: world}")))
	if err != nil || g.Greet() != "hello world" {
		t.Fatalf("got %v, %v", g, err)
	}
	if _, err := r.Build("silent", NewParams("greeting silent", nil)); err != nil {
		t.Fatalf("no params is fine for a strategy that takes none: %v", err)
	}
	if got := strings.Join(r.Names(), ","); got != "hello,silent" || !r.Has("silent") || r.Has("shout") {
		t.Fatalf("Names/Has: %s", got)
	}
}

func TestBuildErrorsNameTheProblem(t *testing.T) {
	r := newTestRegistry()
	cases := []struct {
		name, params, want string
	}{
		{"shout", "", `unknown greeting strategy "shout" (registered: hello, silent)`},
		{"hello", "{nme: world}", "greeting hello: params: yaml: unmarshal errors:\n  line 1: field nme not found"},
		{"hello", "{}", "greeting strategy hello: name is required"},
	}
	for _, c := range cases {
		var n *yaml.Node
		if c.params != "" {
			n = node(t, c.params)
		}
		_, err := r.Build(c.name, NewParams("greeting "+c.name, n))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: want %q, got %v", c.name, c.params, c.want, err)
		}
	}
}

func TestRegisterTwicePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering a name twice must panic")
		}
	}()
	newTestRegistry().Register("hello", nil)
}
