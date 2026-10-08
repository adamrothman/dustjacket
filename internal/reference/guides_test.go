package reference

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
)

var graphqlBlock = regexp.MustCompile("(?s)```graphql\n(.*?)```")

func TestGuidesMatchTopics(t *testing.T) {
	files, err := fs.Glob(guides, "guides/*.md")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(f, "guides/"), ".md"))
	}
	want := append([]string{"overview"}, Topics...)
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("guide files %v, want %v", names, want)
	}
	overview := Overview()
	for _, topic := range Topics {
		if !strings.Contains(overview, "- `"+topic+"`: ") {
			t.Errorf("the overview does not list %s", topic)
		}
		if _, err := Guide(strings.ToUpper(topic)); err != nil {
			t.Error(err)
		}
	}
	if _, err := Guide("nope"); err == nil || !strings.Contains(err.Error(), "library, reading") {
		t.Errorf("unknown topic: %v", err)
	}
}

// Every example in every guide must be a valid operation against the
// schema snapshot, so a refresh that breaks one fails here.
func TestGuideExamplesValidate(t *testing.T) {
	for _, name := range append([]string{"overview"}, Topics...) {
		b, err := guides.ReadFile("guides/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if strings.Contains(text, "<!--") {
			t.Errorf("%s: a live-check marker is still unfilled", name)
		}
		for i, m := range graphqlBlock.FindAllStringSubmatch(text, -1) {
			if _, errs := gqlparser.LoadQuery(Schema, m[1]); errs != nil {
				t.Errorf("%s example %d: %v", name, i+1, errs)
			}
		}
	}
}

var fixedPrivacy = regexp.MustCompile(`privacy_setting_id:\s*\d`)

// Examples that create something take its privacy from the person, never
// a fixed value: a guide that writes 1 publishes a private note.
func TestGuideExamplesDontFixPrivacy(t *testing.T) {
	for _, name := range append([]string{"overview"}, Topics...) {
		b, err := guides.ReadFile("guides/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		for i, m := range graphqlBlock.FindAllStringSubmatch(string(b), -1) {
			if fixedPrivacy.MatchString(m[1]) {
				t.Errorf("%s example %d sets a fixed privacy_setting_id", name, i+1)
			}
		}
	}
}
