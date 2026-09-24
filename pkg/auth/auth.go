// Package auth issues and verifies the JWTs every service trusts. Per
// docs/adr/0001-identity-propagation.md, each service runs Middleware
// itself: identity never comes from request bodies or client-set headers.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Role string

const (
	RoleCustomer   Role = "customer"
	RoleRestaurant Role = "restaurant"
	RoleDriver     Role = "driver"
	RoleAdmin      Role = "admin"
)

// ValidRole reports whether r is one of the four roles above.
func ValidRole(r Role) bool {
	switch r {
	case RoleCustomer, RoleRestaurant, RoleDriver, RoleAdmin:
		return true
	}
	return false
}

type Claims struct {
	UserID string `json:"sub"`
	Role   Role   `json:"role"`
	Name   string `json:"name"`
	// RefID is the domain entity this account acts as: the restaurantId for
	// a restaurant account, the driverId for a driver. Empty for customers
	// and admins. Only an admin can set it (see customer service POST /users).
	RefID string `json:"refId,omitempty"`
	jwt.RegisteredClaims
}

// Is reports whether the caller has any of the given roles.
func (c *Claims) Is(roles ...Role) bool {
	for _, r := range roles {
		if c.Role == r {
			return true
		}
	}
	return false
}

func (c *Claims) IsAdmin() bool { return c.Role == RoleAdmin }

// ActsAs reports whether the caller is the restaurant or driver with the
// given id (their RefID), or an admin. Use it for "may this caller touch
// restaurant X / delivery for driver Y" ownership checks.
func (c *Claims) ActsAs(role Role, refID string) bool {
	if c.IsAdmin() {
		return true
	}
	return c.Role == role && refID != "" && c.RefID == refID
}

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) Issue(userID string, role Role, name, refID string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID, Role: role, Name: name, RefID: refID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
}

var ErrInvalidToken = errors.New("invalid token")

// Verify pins the algorithm to HS256 (so an attacker can't pick "none" or
// swap to a key-confusion attack) and requires an expiry, a subject and a
// known role.
func (i *Issuer) Verify(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims,
		func(*jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.UserID == "" || !ValidRole(claims.Role) {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

type ctxKey int

const claimsKey ctxKey = iota

// WithClaims returns ctx carrying claims. Used by Middleware, and by tests
// that exercise handlers without minting a real token.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

// MustFromContext is for handlers mounted behind Middleware, where a missing
// principal is a wiring bug. It panics loudly rather than letting a handler
// run as nobody; chi's Recoverer turns that into a 500.
func MustFromContext(ctx context.Context) *Claims {
	c, ok := FromContext(ctx)
	if !ok {
		panic("auth: handler reached without auth.Middleware in front of it")
	}
	return c
}

// BearerToken extracts the token from an Authorization header value.
func BearerToken(header string) (string, bool) {
	tok, ok := strings.CutPrefix(header, "Bearer ")
	return tok, ok && tok != ""
}

// Middleware verifies the Bearer token and stores its Claims in the request
// context.
func (i *Issuer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := BearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		claims, err := i.Verify(tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
	})
}

// RequireRole gates a handler to one of the given roles. Mount it after
// Middleware.
func RequireRole(roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok || !claims.Is(roles...) {
				writeErr(w, http.StatusForbidden, "forbidden", "your role may not do this")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeErr mirrors httpx.APIError; auth can't import httpx because httpx
// would then be pulled into every tool that only needs to mint a token.
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
