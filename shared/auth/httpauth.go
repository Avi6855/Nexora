package auth

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Header names used across the platform.
const (
	HeaderAuthorization = "Authorization"
	HeaderUserID        = "X-User-ID"
	HeaderInternalToken = "X-Internal-Token"
	BearerScheme        = "Bearer "
)

// Authenticator verifies inbound requests at the origin (each service),
// never trusting client-supplied identity headers:
//
//   - A valid user JWT (HS256, issuer "nexora", audience "nexora-api")
//     causes the verified "sub" claim to OVERWRITE any client-supplied
//     X-User-ID header before it reaches handlers.
//   - A matching X-Internal-Token (shared secret injected only inside the
//     compose network) lets service-to-service callers through for
//     internal endpoints.
//
// Public prefixes (auth endpoints, health, metrics, webhooks) bypass both.
type Authenticator struct {
	secret         []byte
	internalToken  string
	publicPrefixes []string
	allowInternal  bool
}

// NewAuthenticator builds the middleware. secret is the JWT signing secret;
// internalToken is the optional service-to-service secret; publicPrefixes are
// path prefixes that never require credentials (e.g. "/v1/auth", "/metrics").
func NewAuthenticator(secret, internalToken string, publicPrefixes ...string) *Authenticator {
	return &Authenticator{
		secret:         []byte(secret),
		internalToken:  internalToken,
		publicPrefixes: publicPrefixes,
		allowInternal:  internalToken != "",
	}
}

// Middleware wraps a handler with authentication.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// 1) User JWT — authoritative identity.
		if claims, ok := a.verifyBearer(r.Header.Get(HeaderAuthorization)); ok {
			// Overwrite any caller-supplied value; handlers must never trust
			// the raw client header.
			r.Header.Set(HeaderUserID, claims.Subject)
			r.Header.Del(HeaderInternalToken)
			next.ServeHTTP(w, r)
			return
		}

		// 2) Internal service token (compose-network secret).
		if a.allowInternal && a.validInternalToken(r.Header.Get(HeaderInternalToken)) {
			r.Header.Del(HeaderUserID)
			next.ServeHTTP(w, r)
			return
		}

		writeUnauthorized(w)
	})
}

func (a *Authenticator) isPublic(path string) bool {
	for _, p := range a.publicPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (a *Authenticator) verifyBearer(authz string) (*Claims, bool) {
	if !strings.HasPrefix(authz, BearerScheme) {
		return nil, false
	}
	tokenString := strings.TrimPrefix(authz, BearerScheme)
	if tokenString == "" {
		return nil, false
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return a.secret, nil
	}, jwt.WithIssuer("nexora"), jwt.WithAudience("nexora-api"), jwt.WithValidMethods([]string{"HS256"}))

	if err != nil || !token.Valid {
		return nil, false
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || claims.Subject == "" {
		return nil, false
	}
	return claims, true
}

func (a *Authenticator) validInternalToken(got string) bool {
	if got == "" || a.internalToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.internalToken)) == 1
}

// InternalToken returns the shared service-to-service secret injected only
// inside the compose network. Callers on the public internet never see it.
func InternalToken() string {
	return os.Getenv("INTERNAL_TOKEN")
}

// AddInternalToken attaches the service-to-service credential to an outbound
// request so downstream services can authenticate machine-to-machine calls.
func AddInternalToken(req *http.Request) {
	if tok := InternalToken(); tok != "" {
		req.Header.Set(HeaderInternalToken, tok)
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","message":"valid credentials are required"}`))
}
