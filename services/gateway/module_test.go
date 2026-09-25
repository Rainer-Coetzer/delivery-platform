package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBlockInternal(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := blockInternal(ok)
	for path, want := range map[string]int{"/auth/verify": 404, "/auth/login": 200, "/jobs": 200} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}
