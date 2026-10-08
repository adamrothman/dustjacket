package reference

import (
	"embed"
	"fmt"
	"slices"
	"strings"
)

//go:embed guides/*.md
var guides embed.FS

// Topics are the guides besides the overview, in the order the overview
// lists them.
var Topics = []string{"library", "reading", "search", "catalog", "lists", "goals", "journal", "social", "recommendations", "limits"}

// Overview is where Claude starts: the tools, the ids, the topics, the
// traps.
func Overview() string {
	b, err := guides.ReadFile("guides/overview.md")
	if err != nil {
		panic(err) // embedded; the tests read every guide
	}
	return string(b)
}

// Guide returns one topic's guide.
func Guide(topic string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(topic))
	if !slices.Contains(Topics, t) {
		return "", fmt.Errorf("there is no guide %q; the topics are %s", topic, strings.Join(Topics, ", "))
	}
	b, err := guides.ReadFile("guides/" + t + ".md")
	return string(b), err
}
