package main

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("DUSTJACKET_BASE_URL", "https://dustjacket.rothman.tools/")
	t.Setenv("DUSTJACKET_ADMIN_IDS", " 7, 42 ,,")
	t.Setenv("DUSTJACKET_OAUTH_REDIRECT_HOSTS", "")
	c := loadConfig()
	if c.BaseURL != "https://dustjacket.rothman.tools" {
		t.Errorf("base URL %q", c.BaseURL)
	}
	if !slices.Equal(c.AdminIDs, []string{"7", "42"}) {
		t.Errorf("admins %q", c.AdminIDs)
	}
	if !slices.Equal(c.RedirectHosts, []string{"claude.ai", "claude.com"}) || c.Table != "dustjacket" || c.HardcoverURL != "https://api.hardcover.app/v1/graphql" {
		t.Errorf("defaults: %+v", c)
	}
}

func TestKMSKeysAreARNs(t *testing.T) {
	t.Setenv("DUSTJACKET_KMS_KEY_ID", "arn:aws:kms:us-west-2:1:key/new")
	t.Setenv("DUSTJACKET_KMS_PREVIOUS_KEY_IDS", "arn:aws:kms:us-west-2:1:key/old, ")
	c := loadConfig()
	if c.KMSKeyID != "arn:aws:kms:us-west-2:1:key/new" || !slices.Equal(c.KMSPreviousKeyIDs, []string{"arn:aws:kms:us-west-2:1:key/old"}) {
		t.Fatalf("keys: %q %q", c.KMSKeyID, c.KMSPreviousKeyIDs)
	}
	// KMS reports the sealing key as an ARN; an alias or bare ID here would
	// make every stored key look sealed by a stranger.
	for _, bad := range []string{"alias/dustjacket", "1234abcd-12ab-34cd-56ef-1234567890ab", "arn:aws:kms:us-west-2:1:alias/dustjacket"} {
		t.Setenv("DUSTJACKET_KMS_KEY_ID", bad)
		if _, _, err := build(context.Background(), false); err == nil || !strings.Contains(err.Error(), "key ARN") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	t.Setenv("DUSTJACKET_KMS_KEY_ID", "arn:aws:kms:us-west-2:1:key/new")
	t.Setenv("DUSTJACKET_KMS_PREVIOUS_KEY_IDS", "arn:aws:kms:us-west-2:1:alias/old")
	if _, _, err := build(context.Background(), false); err == nil || !strings.Contains(err.Error(), "key ARN") {
		t.Errorf("previous alias: %v", err)
	}
}

// An admin given by username would never match, and is refused rather
// than leave nobody able to manage the allowlist.
func TestAdminsAreUserIDs(t *testing.T) {
	for _, bad := range []string{"adam", "7,adam", "-7", "7.0"} {
		t.Setenv("DUSTJACKET_ADMIN_IDS", bad)
		if _, _, err := build(context.Background(), true); err == nil || !strings.Contains(err.Error(), "Hardcover user IDs") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	t.Setenv("DUSTJACKET_ADMIN_IDS", "7")
	if _, _, err := build(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}
