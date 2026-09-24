package app

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
)

// InternalBaseURL is the host other modules are "called" on inside the
// platform binary. It never resolves: the in-memory transport intercepts it.
const InternalBaseURL = "http://platform.internal"

// inprocTransport serves HTTP requests by calling the platform's own router
// directly: no socket, no port (ADR-0002). Calls still pass through each
// module's auth middleware, so the callee checks the forwarded user's token
// exactly as it would across the network (ADR-0001).
//
// Not for streaming responses or WebSockets: the response is buffered. No
// inter-module call needs either.
type inprocTransport struct{ h http.Handler }

func (t inprocTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	if r.Body == nil {
		r.Body = http.NoBody
	}
	r.RequestURI = r.URL.RequestURI()
	r.RemoteAddr = "inproc"

	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	t.h.ServeHTTP(rec, r)

	return &http.Response{
		Status:        strconv.Itoa(rec.status) + " " + http.StatusText(rec.status),
		StatusCode:    rec.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rec.header,
		Body:          io.NopCloser(bytes.NewReader(rec.body.Bytes())),
		ContentLength: int64(rec.body.Len()),
		Request:       req,
	}, nil
}

// recorder is a minimal http.ResponseWriter. (httptest.ResponseRecorder
// would do, but it's a test package and its behaviour may change for tests'
// sake.)
type recorder struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status, r.wroteHeader = code, true
}

func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}

// NewInprocClient returns an http.Client that serves every request with h.
func NewInprocClient(h http.Handler) *http.Client {
	return &http.Client{Transport: inprocTransport{h: h}}
}
