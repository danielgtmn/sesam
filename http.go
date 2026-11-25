package sesam

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// HandlerFunc is an endpoint of a plugin. A returned *Error or
// *RateLimitError is answered with its status and message; any other error
// is logged and answered with a generic 500.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Router collects the endpoints under Config.BasePath.
type Router struct {
	a   *Auth
	mux *http.ServeMux
}

func newRouter(a *Auth) *Router {
	r := &Router{a: a, mux: http.NewServeMux()}
	r.Handle("GET", "/session", a.handleSession)
	r.Handle("POST", "/sign-out", a.handleSignOut)
	r.Handle("GET", "/sessions", a.handleSessions)
	r.Handle("POST", "/sessions/revoke", a.handleRevokeSession)
	r.Handle("POST", "/sessions/revoke-others", a.handleRevokeOthers)
	return r
}

// Handle registers an endpoint; path is relative to Config.BasePath, e.g.
// "/sign-in".
func (r *Router) Handle(method, path string, h HandlerFunc) {
	r.mux.HandleFunc(method+" "+r.a.basePath+path, func(w http.ResponseWriter, req *http.Request) {
		if err := h(w, req); err != nil {
			r.a.WriteError(w, req, err)
		}
	})
}

// Handler serves the endpoints of sesam and its plugins under
// Config.BasePath. Mount it there: mux.Handle("/api/auth/", a.Handler()).
// Changing requests must come from a trusted origin (see Config).
func (a *Auth) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := a.CheckRequest(r); err != nil {
			a.WriteError(w, r, err)
			return
		}
		a.router.mux.ServeHTTP(w, r)
	})
}

func isSafe(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// CheckRequest refuses changing requests that come from a page that is not
// trusted: the Origin header, if sent, must be the BaseURL's origin or a
// trusted one, and Config.RequireHeader must be set. Handler runs it for
// every request; apps can use it for their own endpoints.
func (a *Auth) CheckRequest(r *http.Request) error {
	if isSafe(r.Method) {
		return nil
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !a.origins[origin(u)] {
			return ErrForbiddenOrigin
		}
	} else if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return ErrForbiddenOrigin
	}
	if a.cfg.RequireHeader != "" && r.Header.Get(a.cfg.RequireHeader) == "" {
		return ErrForbiddenOrigin
	}
	return nil
}

// WriteError answers with the status and message of err (see HandlerFunc).
func (a *Auth) WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var rl *RateLimitError
	var e *Error
	switch {
	case errors.As(err, &rl):
		secs := int(rl.RetryAfter.Seconds() + 0.999)
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": rl.Error(), "code": ErrRateLimited.Code})
	case errors.As(err, &e) && e.Status != 0:
		WriteJSON(w, e.Status, map[string]string{"error": e.Message, "code": e.Code})
	default:
		a.logger.ErrorContext(r.Context(), "sesam: request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong. Please try again.", "code": "internal"})
	}
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ReadJSON decodes a JSON request body of at most 64 KiB into v; unknown
// fields are refused.
func ReadJSON(r *http.Request, v any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return InvalidInput("Send the request as JSON.")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return InvalidInput("The request could not be read.")
	}
	return nil
}

// Meta returns the device information of a request for a new session.
func (a *Auth) Meta(r *http.Request) SessionMeta {
	return SessionMeta{IP: a.cfg.ClientIP(r), UserAgent: r.UserAgent()}
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CookieName is the name the session cookie is sent with.
func (a *Auth) CookieName() string {
	if a.secure {
		return "__Host-" + a.cfg.Cookie.Name
	}
	return a.cfg.Cookie.Name
}

// SetSessionCookie sends the session token as an HttpOnly cookie that lasts
// as long as the session can at most.
func (a *Auth) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: a.CookieName(), Value: token, Path: "/",
		MaxAge:   int(a.cfg.Session.MaxLifetime.Seconds()),
		HttpOnly: true, Secure: a.secure, SameSite: a.cfg.Cookie.SameSite,
	})
}

// ClearSessionCookie removes the session cookie.
func (a *Auth) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: a.CookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.secure, SameSite: a.cfg.Cookie.SameSite,
	})
}

// SessionToken returns the token from the request's session cookie.
func (a *Auth) SessionToken(r *http.Request) string {
	c, err := r.Cookie(a.CookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

// SessionFromRequest returns the session of the request's cookie, or
// ErrUnauthenticated.
func (a *Auth) SessionFromRequest(r *http.Request) (Session, User, error) {
	if s, ok := FromContext(r.Context()); ok {
		return s.Session, s.User, nil
	}
	return a.Session(r.Context(), a.SessionToken(r))
}

// Current is the signed-in person of a request.
type Current struct {
	Session Session
	User    User
}

type ctxKey struct{}

// FromContext returns what Middleware stored for a signed-in request.
func FromContext(ctx context.Context) (Current, bool) {
	c, ok := ctx.Value(ctxKey{}).(Current)
	return c, ok
}

// WithCurrent stores c in ctx, e.g. for tests of handlers behind Middleware.
func WithCurrent(ctx context.Context, c Current) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// Middleware looks up the session cookie and, if it is valid, stores the
// session and user for FromContext. Requests without a valid session pass
// through unchanged; use RequireSession to refuse them. Changing requests
// with a session cookie must pass CheckRequest.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := a.SessionToken(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if err := a.CheckRequest(r); err != nil {
			a.WriteError(w, r, err)
			return
		}
		s, u, err := a.Session(r.Context(), token)
		switch {
		case errors.Is(err, ErrUnauthenticated):
			next.ServeHTTP(w, r)
		case err != nil:
			a.WriteError(w, r, err)
		default:
			next.ServeHTTP(w, r.WithContext(WithCurrent(r.Context(), Current{s, u})))
		}
	})
}

// RequireSession answers 401 unless Middleware found a valid session.
func (a *Auth) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			a.WriteError(w, r, ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sessionView is a session as the endpoints show it.
type sessionView struct {
	Session
	Current bool `json:"current"`
}

func (a *Auth) handleSession(w http.ResponseWriter, r *http.Request) error {
	s, u, err := a.SessionFromRequest(r)
	if err != nil {
		return err
	}
	WriteJSON(w, http.StatusOK, map[string]any{"user": u, "session": s})
	return nil
}

func (a *Auth) handleSignOut(w http.ResponseWriter, r *http.Request) error {
	if err := a.SignOut(r.Context(), a.SessionToken(r)); err != nil {
		return err
	}
	a.ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *Auth) handleSessions(w http.ResponseWriter, r *http.Request) error {
	cur, u, err := a.SessionFromRequest(r)
	if err != nil {
		return err
	}
	list, err := a.Sessions(r.Context(), u.ID)
	if err != nil {
		return err
	}
	out := make([]sessionView, len(list))
	for i, s := range list {
		out[i] = sessionView{s, s.ID == cur.ID}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
	return nil
}

func (a *Auth) handleRevokeSession(w http.ResponseWriter, r *http.Request) error {
	_, u, err := a.SessionFromRequest(r)
	if err != nil {
		return err
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := ReadJSON(r, &body); err != nil {
		return err
	}
	if err := a.RevokeSession(r.Context(), u.ID, body.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *Auth) handleRevokeOthers(w http.ResponseWriter, r *http.Request) error {
	cur, u, err := a.SessionFromRequest(r)
	if err != nil {
		return err
	}
	if _, err := a.RevokeUserSessions(r.Context(), u.ID, cur.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
