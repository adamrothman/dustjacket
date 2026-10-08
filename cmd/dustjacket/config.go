package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/mcpserver"
	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
	"github.com/adamrothman/dustjacket/internal/web"
)

// config is the DUSTJACKET_* environment.
type config struct {
	BaseURL           string
	Table             string
	KMSKeyID          string
	KMSPreviousKeyIDs []string
	AdminIDs          []string
	OriginVerify      string
	RedirectHosts     []string
	HardcoverURL      string
}

func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv("DUSTJACKET_" + name)); v != "" {
		return v
	}
	return def
}

func list(s string) []string {
	var out []string
	for x := range strings.SplitSeq(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func loadConfig() config {
	return config{
		BaseURL:           strings.TrimSuffix(env("BASE_URL", "http://localhost:8080"), "/"),
		Table:             env("TABLE", "dustjacket"),
		KMSKeyID:          env("KMS_KEY_ID", ""),
		KMSPreviousKeyIDs: list(env("KMS_PREVIOUS_KEY_IDS", "")),
		AdminIDs:          list(env("ADMIN_IDS", "")),
		OriginVerify:      env("ORIGIN_VERIFY", ""),
		RedirectHosts:     list(env("OAUTH_REDIRECT_HOSTS", "claude.ai,claude.com")),
		HardcoverURL:      env("HARDCOVER_URL", hardcover.DefaultURL),
	}
}

// version is the commit the binary was built from, or "dev".
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

// build wires everything. With memory it needs no AWS: an in-memory store
// and a sealer whose key dies with the process.
func build(ctx context.Context, memory bool) (http.Handler, config, error) {
	cfg := loadConfig()
	var st store.Store
	var sl sealer.Sealer
	if memory {
		st, sl = store.NewMemory(), sealer.NewLocal()
	} else {
		if cfg.KMSKeyID == "" {
			return nil, cfg, errors.New("DUSTJACKET_KMS_KEY_ID is required")
		}
		// KMS reports the key that sealed a value as its key ARN (never an
		// alias's), and a stored key opens only if that ARN is one of these.
		for _, id := range append([]string{cfg.KMSKeyID}, cfg.KMSPreviousKeyIDs...) {
			if !strings.HasPrefix(id, "arn:aws:kms:") || !strings.Contains(id, ":key/") {
				return nil, cfg, fmt.Errorf("KMS keys must be given as key ARNs, not %q", id)
			}
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, cfg, err
		}
		st = store.NewDynamo(dynamodb.NewFromConfig(awsCfg), cfg.Table)
		sl = &sealer.KMS{Client: kms.NewFromConfig(awsCfg), KeyID: cfg.KMSKeyID, Previous: cfg.KMSPreviousKeyIDs}
	}
	// Admins are Hardcover user IDs, which never change, unlike usernames.
	for _, id := range cfg.AdminIDs {
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return nil, cfg, fmt.Errorf("admins must be given as Hardcover user IDs, not %q", id)
		}
	}
	if len(cfg.AdminIDs) == 0 {
		slog.Warn("DUSTJACKET_ADMIN_IDS is empty: nobody can manage the allowlist")
	}
	if env("ALLOWED_USERS", "") != "" {
		slog.Warn("DUSTJACKET_ALLOWED_USERS is no longer read: the allowlist is in the table (docs/decisions.md 23)")
	}
	oa := &oauth.Server{Store: st, Sealer: sl, Issuer: cfg.BaseURL, AllowedRedirectHosts: cfg.RedirectHosts, Admins: cfg.AdminIDs}
	hc := &hardcover.Client{URL: cfg.HardcoverURL, UserAgent: "dustjacket/" + version() + " (+" + cfg.BaseURL + ")"}
	srv := &web.Server{
		OAuth:        oa,
		Hardcover:    hc,
		MCP:          &mcpserver.Handler{OAuth: oa, Hardcover: hc, Version: version()},
		BaseURL:      cfg.BaseURL,
		OriginVerify: cfg.OriginVerify,
	}
	return srv.Handler(), cfg, nil
}
