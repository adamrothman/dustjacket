// Package web is Dustjacket's HTTP surface: the OAuth endpoints and the
// connect page where a person pastes their Hardcover key, /mcp, a home
// page, and the middleware every request passes through.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/reqlog"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Server struct {
	OAuth     *oauth.Server
	Hardcover *hardcover.Client
	MCP       http.Handler
	// BaseURL is the public origin, "https://dustjacket.rothman.tools".
	BaseURL string
	// OriginVerify, when set, must match the X-Origin-Verify header
	// CloudFront adds, so the raw function URL is not a back door.
	OriginVerify string
	Log          *slog.Logger

	tmpl map[string]*template.Template
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	s.parseTemplates()
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(static)))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { s.render(w, http.StatusOK, "home", s.BaseURL) })

	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.OAuth.Metadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.OAuth.ProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.OAuth.ProtectedResourceMetadata)
	mux.HandleFunc("POST /oauth/register", s.OAuth.Register)
	mux.HandleFunc("POST /oauth/token", s.OAuth.Token)
	mux.HandleFunc("POST /oauth/revoke", s.OAuth.Revoke)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/authorize", s.connect)
	mux.Handle("/mcp", s.MCP)

	return s.accessLog(s.recoverer(s.originVerify(s.headers(mux))))
}

// accessLog writes one line per request, with whatever the handlers added
// through reqlog. It logs the path and never the query, which carries
// OAuth codes, nor any body, which can carry a Hardcover key.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, fields := reqlog.With(r.Context())
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r.WithContext(ctx))
		attrs := []any{"method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds()}
		if m := r.Header.Get("Mcp-Method"); m != "" {
			attrs = append(attrs, "mcp_method", m)
		}
		if v := r.Header.Get("MCP-Protocol-Version"); v != "" {
			attrs = append(attrs, "mcp_version", v)
		}
		s.log().Info("request", append(attrs, fields.Attrs()...)...)
	})
}

// statusWriter records the status a handler sends; one that never calls
// WriteHeader sends 200.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController (the MCP SDK flushes through one)
// reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log().Error("panic", "path", r.URL.Path, "err", rec)
				http.Error(w, "something went wrong", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originVerify(next http.Handler) http.Handler {
	if s.OriginVerify == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Origin-Verify") != s.OriginVerify {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// headers sets the security headers. The CSP has no form-action: the
// connect form's redirect to the client's callback would be subject to
// it, hop by hop, and the page has nothing a form could be injected into.
func (s *Server) headers(next http.Handler) http.Handler {
	const csp = "default-src 'none'; style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if strings.HasPrefix(s.BaseURL, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// same-origin, not no-referrer: under no-referrer browsers send
		// "Origin: null" with the connect form, which the origin check
		// rejects. Cross-origin links (Hardcover's key page) get nothing.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// cacheStatic lets browsers keep the stylesheet for an hour.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Server) parseTemplates() {
	s.tmpl = map[string]*template.Template{}
	pages, _ := fs.Glob(templateFS, "templates/*.html")
	for _, p := range pages {
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		s.tmpl[name] = template.Must(template.ParseFS(templateFS, "templates/layout.html", p))
	}
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.tmpl[name].ExecuteTemplate(w, "layout.html", data); err != nil {
		s.log().Error("render", "template", name, "err", err)
	}
}
