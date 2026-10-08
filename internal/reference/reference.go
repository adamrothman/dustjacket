// Package reference is what hardcover_docs serves: guides written for
// Claude (guides.go), and definitions looked up in a snapshot of
// Hardcover's GraphQL schema (from hardcoverapp/hardcover-docs, MIT;
// refreshed by scripts/update-schema.sh).
package reference

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/agnivade/levenshtein"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

//go:embed schema.graphql
var schemaSDL string

// Schema is Hardcover's schema as of the snapshot.
var Schema = gqlparser.MustLoadSchema(&ast.Source{Name: "schema.graphql", Input: schemaSDL})

// Lookup returns the definition of every type and root field with this
// name: Hasura often gives a table's object type and its query root field
// the same name. "query_root.<field>" or "mutation_root.<field>" narrows
// it to one root field. Unknown names get suggestions.
func Lookup(name string) (string, error) {
	name = strings.TrimSpace(name)
	if root, field, ok := strings.Cut(name, "."); ok {
		def := Schema.Types[root]
		if def == nil || (def != Schema.Query && def != Schema.Mutation) {
			return "", fmt.Errorf("%q is not query_root or mutation_root", root)
		}
		if f := def.Fields.ForName(field); f != nil {
			return root + "." + signature(f), nil
		}
		return "", notFound(name, fieldNames(def))
	}
	var parts []string
	if d := Schema.Types[name]; d != nil {
		parts = append(parts, printType(d))
	}
	for _, def := range []*ast.Definition{Schema.Query, Schema.Mutation} {
		if f := def.Fields.ForName(name); f != nil {
			parts = append(parts, def.Name+"."+signature(f))
		}
	}
	if len(parts) == 0 {
		return "", notFound(name, allNames())
	}
	return strings.Join(parts, "\n\n"), nil
}

// printType renders a definition as compact SDL: one line per field,
// arguments inline, no descriptions (Hasura's are boilerplate).
func printType(d *ast.Definition) string {
	var b strings.Builder
	switch d.Kind {
	case ast.Scalar:
		fmt.Fprintf(&b, "scalar %s", d.Name)
	case ast.Union:
		fmt.Fprintf(&b, "union %s = %s", d.Name, strings.Join(d.Types, " | "))
	case ast.Enum:
		fmt.Fprintf(&b, "enum %s {\n", d.Name)
		for _, v := range d.EnumValues {
			fmt.Fprintf(&b, "  %s\n", v.Name)
		}
		b.WriteString("}")
	default:
		keyword := map[ast.DefinitionKind]string{ast.Object: "type", ast.Interface: "interface", ast.InputObject: "input"}[d.Kind]
		fmt.Fprintf(&b, "%s %s {\n", keyword, d.Name)
		for _, f := range d.Fields {
			if !strings.HasPrefix(f.Name, "__") {
				fmt.Fprintf(&b, "  %s\n", signature(f))
			}
		}
		b.WriteString("}")
	}
	return b.String()
}

func signature(f *ast.FieldDefinition) string {
	if len(f.Arguments) == 0 {
		return f.Name + ": " + f.Type.String()
	}
	args := make([]string, len(f.Arguments))
	for i, a := range f.Arguments {
		args[i] = a.Name + ": " + a.Type.String()
	}
	return f.Name + "(" + strings.Join(args, ", ") + "): " + f.Type.String()
}

func fieldNames(d *ast.Definition) []string {
	var out []string
	for _, f := range d.Fields {
		if !strings.HasPrefix(f.Name, "__") {
			out = append(out, f.Name)
		}
	}
	return out
}

// allNames is every type and root field name, once each.
func allNames() []string {
	seen := map[string]bool{}
	for name := range Schema.Types {
		if !strings.HasPrefix(name, "__") {
			seen[name] = true
		}
	}
	for _, def := range []*ast.Definition{Schema.Query, Schema.Mutation} {
		for _, n := range fieldNames(def) {
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

// notFound suggests the five closest names, ignoring case.
func notFound(name string, candidates []string) error {
	type scored struct {
		name string
		d    int
	}
	lower := strings.ToLower(name)
	s := make([]scored, len(candidates))
	for i, c := range candidates {
		s[i] = scored{c, levenshtein.ComputeDistance(lower, strings.ToLower(c))}
	}
	sort.Slice(s, func(i, j int) bool {
		if s[i].d != s[j].d {
			return s[i].d < s[j].d
		}
		return s[i].name < s[j].name
	})
	names := make([]string, 0, 5)
	for i := 0; i < len(s) && i < 5; i++ {
		names = append(names, s[i].name)
	}
	return fmt.Errorf("nothing is named %q; did you mean %s?", name, strings.Join(names, ", "))
}
