// Package auth issues and verifies the JWTs the gateway hands out and every
// downstream service trusts. Roles gate which endpoints a token may call.
package auth

import (
	"context"
	"errors"
	"fmt"
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

type Claims struct {
	UserID string `json:"sub"`
	Role   Role   `json:"role"`
	Name   string `json:"name"`
	// RefID is the id of the domain entity this token acts as, e.g. the
	// restaurantId for a restaurant-role token or driverId for a driver.
	RefID string `json:"refId,omitempty"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) Issue(userID string, role Role, name, refID string) (string, error) {
	claims := Claims{
		UserID: userID,
		Role:   role,
		Name:   name,
		RefID:  refID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(i.ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(i.secret)
}

var ErrInvalidToken = errors.New("invalid token")

func (i *Issuer) Verify(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrInvalidToken)
		}
		return i.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

type ctxKey int

const claimsKey ctxKey = iota

// Middleware verifies the Bearer token and stores its Claims in the request
// context. Use RequireRole after it to gate specific routes.
func (i *Issuer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		tokenStr, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || tokenStr == "" {
			http.Error(w, `{"error":"unauthorized","message":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		claims, err := i.Verify(tokenStr)
		if err != nil {
			http.Error(w, `{"error":"unauthorized","message":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

// RequireRole gates a handler to one of the given roles.
func RequireRole(roles ...Role) func(http.Handler) http.Handler {
	allowed := make(map[Role]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok || !allowed[claims.Role] {
				http.Error(w, `{"error":"forbidden","message":"role not permitted"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
