package reference

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		// A table: its object type and its query root field.
		{"user_books", []string{"type user_books {\n", "  status_id: Int!\n", "query_root.user_books(distinct_on: [user_books_select_column!], limit: Int, offset: Int, order_by: [user_books_order_by!], where: user_books_bool_exp): [user_books!]!"}},
		{"mutation_root.update_user_book", []string{"mutation_root.update_user_book(id: Int!, object: UserBookUpdateInput!): UserBookIdType"}},
		{"update_user_book", []string{"mutation_root.update_user_book(id: Int!, object: UserBookUpdateInput!): UserBookIdType"}},
		{"UserBookUpdateInput", []string{"input UserBookUpdateInput {\n", "  status_id: Int\n", "  rating: numeric\n"}},
		{"TrendingDuration", []string{"enum TrendingDuration {\n", "  month\n"}},
		{"citext", []string{"scalar citext"}},
		{" query_root.me ", []string{"query_root.me(distinct_on: [users_select_column!], limit: Int, offset: Int, order_by: [users_order_by!], where: users_bool_exp): [users!]!"}},
	} {
		got, err := Lookup(tc.name)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: lacks %q in:\n%s", tc.name, w, got)
			}
		}
		if strings.Contains(got, `"""`) {
			t.Errorf("%s: descriptions leaked in", tc.name)
		}
	}
}

func TestLookupSuggests(t *testing.T) {
	for name, want := range map[string]string{
		"UserBooks":                  "user_books",
		"query_root.user_book":       "user_books",
		"mutation_root.insert_bookz": "insert_book",
	} {
		_, err := Lookup(name)
		if err == nil || !strings.Contains(err.Error(), "did you mean") || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Lookup("users.id"); err == nil || !strings.Contains(err.Error(), "not query_root or mutation_root") {
		t.Errorf("users.id: %v", err)
	}
}
