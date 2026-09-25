package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const secret = "test-secret-that-is-at-least-32-bytes-long"

func sign(t *testing.T, method jwt.SigningMethod, key any, claims Claims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validClaims() Claims {
	return Claims{UserID: "u1", Role: RoleCustomer, RegisteredClaims: jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
}

func TestMiddleware(t *testing.T) {
	iss := NewIssuer(secret, time.Hour)
	good, _ := iss.Issue("u1", RoleCustomer, "Nangula", "")

	expired := validClaims()
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))

	noExp := validClaims()
	noExp.ExpiresAt = nil

	badRole := validClaims()
	badRole.Role = "superadmin"

	noSub := validClaims()
	noSub.UserID = ""

	good512 := sign(t, jwt.SigningMethodHS512, []byte(secret), validClaims())
	noneAlg := sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims())

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"valid", "Bearer " + good, 200},
		{"missing header", "", 401},
		{"not bearer", "Basic " + good, 401},
		{"tampered", "Bearer " + good[:len(good)-2] + "xx", 401},
		{"wrong secret", "Bearer " + sign(t, jwt.SigningMethodHS256, []byte("another-secret-another-secret-xx"), validClaims()), 401},
		{"expired", "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(secret), expired), 401},
		{"no expiry", "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(secret), noExp), 401},
		{"alg HS512 not allowed", "Bearer " + good512, 401},
		{"alg none", "Bearer " + noneAlg, 401},
		{"unknown role", "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(secret), badRole), 401},
		{"no subject", "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(secret), noSub), 401},
	}
	h := iss.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := MustFromContext(r.Context())
		if c.UserID != "u1" {
			t.Errorf("principal user = %q", c.UserID)
		}
	}))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
			if c.want != 200 && !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
				t.Fatal("errors must use the JSON error shape")
			}
		})
	}
}

func TestRequireRoleAndActsAs(t *testing.T) {
	h := RequireRole(RoleAdmin, RoleMerchant)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for role, want := range map[Role]int{RoleAdmin: 200, RoleMerchant: 200, RoleCustomer: 403, RoleAgent: 403} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(WithClaims(req.Context(), &Claims{UserID: "u", Role: role}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("role %s: status %d, want %d", role, rec.Code, want)
		}
	}

	cases := []struct {
		name  string
		c     Claims
		role  Role
		ref   string
		wants bool
	}{
		{"own merchant", Claims{Role: RoleMerchant, RefID: "r1"}, RoleMerchant, "r1", true},
		{"other merchant", Claims{Role: RoleMerchant, RefID: "r1"}, RoleMerchant, "r2", false},
		{"unlinked merchant never matches empty id", Claims{Role: RoleMerchant}, RoleMerchant, "", false},
		{"agent claiming merchant", Claims{Role: RoleAgent, RefID: "r1"}, RoleMerchant, "r1", false},
		{"admin acts as anyone", Claims{Role: RoleAdmin}, RoleMerchant, "r9", true},
	}
	for _, c := range cases {
		if got := c.c.ActsAs(c.role, c.ref); got != c.wants {
			t.Errorf("%s: ActsAs = %v, want %v", c.name, got, c.wants)
		}
	}
}
