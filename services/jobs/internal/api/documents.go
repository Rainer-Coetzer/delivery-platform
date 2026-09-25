package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/blob"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

// Document is a file on a job.
type Document struct {
	ID           string    `json:"id"`
	JobID        string    `json:"jobId"`
	Name         string    `json:"name"`
	ContentType  string    `json:"contentType"`
	SizeBytes    int64     `json:"sizeBytes"`
	UploadedBy   string    `json:"uploadedBy"`
	UploadedRole string    `json:"uploadedRole"`
	CreatedAt    time.Time `json:"createdAt"`
	blobKey      string
}

// maxDocName is a storage sanity bound.
const maxDocName = 120

// uploadDocument takes a file for a job from anyone who can see the job:
// the customer's customs form, the merchant's invoice, the agent's signed
// waybill. Type, size and count come from settings.documents. The body is
// the file itself; its name is ?name=.
func (a *API) uploadDocument(w http.ResponseWriter, r *http.Request) {
	if !a.Pack.Has(packs.ModDocuments) {
		httpx.NotFound(w, "this platform has no documents")
		return
	}
	limits := a.Pack.Settings.Documents
	ctx := r.Context()
	caller := auth.MustFromContext(ctx)
	job, ok := a.visibleJob(w, r)
	if !ok {
		return
	}
	name, err := docName(r.URL.Query().Get("name"))
	if err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	ct = strings.ToLower(strings.TrimSpace(ct))
	if !slices.Contains(limits.AllowedTypes, ct) {
		httpx.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "documents may be: "+strings.Join(limits.AllowedTypes, ", "))
		return
	}
	var count int
	if err := a.Pool.QueryRow(ctx, `SELECT count(*) FROM job_documents WHERE job_id = $1`, job.ID).Scan(&count); err != nil {
		a.Log.Error("count documents", "error", err)
		httpx.Internal(w, "could not store the document")
		return
	}
	if count >= limits.MaxPerJob {
		httpx.Conflict(w, fmt.Sprintf("a job holds at most %d documents", limits.MaxPerJob))
		return
	}

	doc := &Document{ID: uuid.NewString(), JobID: job.ID, Name: name, ContentType: ct, UploadedBy: caller.UserID, UploadedRole: string(caller.Role)}
	doc.blobKey = "documents/" + strings.ToLower(job.ID) + "/" + doc.ID
	// The file goes first; a row that fails after it leaves an orphan
	// file, which is cheaper than a row pointing at nothing.
	doc.SizeBytes, err = a.Blobs.Put(ctx, doc.blobKey, r.Body, limits.MaxBytes)
	if errors.Is(err, blob.ErrTooLarge) {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("documents are at most %d bytes", limits.MaxBytes))
		return
	}
	if err != nil {
		a.Log.Error("store document", "jobId", job.ID, "error", err)
		httpx.BadRequest(w, "could not store the document: "+err.Error())
		return
	}
	err = a.Pool.QueryRow(ctx, `
		INSERT INTO job_documents (id, job_id, name, content_type, size_bytes, blob_key, uploaded_by, uploaded_role)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
		doc.ID, doc.JobID, doc.Name, doc.ContentType, doc.SizeBytes, doc.blobKey, doc.UploadedBy, doc.UploadedRole).Scan(&doc.CreatedAt)
	if err != nil {
		a.Log.Error("record document", "jobId", job.ID, "error", err)
		httpx.Internal(w, "could not store the document")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, doc)
}

func (a *API) listDocuments(w http.ResponseWriter, r *http.Request) {
	if !a.Pack.Has(packs.ModDocuments) {
		httpx.NotFound(w, "this platform has no documents")
		return
	}
	job, ok := a.visibleJob(w, r)
	if !ok {
		return
	}
	docs, err := a.documents(r.Context(), job.ID, "")
	if err != nil {
		a.Log.Error("list documents", "error", err)
		httpx.Internal(w, "could not list documents")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, docs)
}

func (a *API) downloadDocument(w http.ResponseWriter, r *http.Request) {
	if !a.Pack.Has(packs.ModDocuments) {
		httpx.NotFound(w, "this platform has no documents")
		return
	}
	job, ok := a.visibleJob(w, r)
	if !ok {
		return
	}
	docs, err := a.documents(r.Context(), job.ID, chi.URLParam(r, "docId"))
	if err != nil || len(docs) != 1 {
		httpx.NotFound(w, "document not found")
		return
	}
	d := docs[0]
	f, err := a.Blobs.Open(r.Context(), d.blobKey)
	if err != nil {
		a.Log.Error("open document", "key", d.blobKey, "error", err)
		httpx.Internal(w, "could not load the document")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", d.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// attachment, not inline: an uploaded file is never rendered as a page
	// of this site.
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.Name+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// documents lists a job's documents, or just one when id is set.
func (a *API) documents(ctx context.Context, jobID, id string) ([]Document, error) {
	rows, err := a.Pool.Query(ctx, `
		SELECT id, job_id, name, content_type, size_bytes, blob_key, uploaded_by, uploaded_role, created_at
		FROM job_documents WHERE job_id = $1 AND ($2 = '' OR id = $2) ORDER BY created_at`, jobID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.JobID, &d.Name, &d.ContentType, &d.SizeBytes, &d.blobKey, &d.UploadedBy, &d.UploadedRole, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// visibleJob loads {id} for a caller who may see it (else 403/404).
func (a *API) visibleJob(w http.ResponseWriter, r *http.Request) (*domain.Job, bool) {
	job, err := a.Store.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		httpx.NotFound(w, "job not found")
		return nil, false
	}
	if err != nil {
		a.Log.Error("load job", "error", err)
		httpx.Internal(w, "could not load job")
		return nil, false
	}
	if !CanView(auth.MustFromContext(r.Context()), job) {
		httpx.Forbidden(w, "this isn't your job")
		return nil, false
	}
	return job, true
}

// docName keeps a file name to something that is safe to echo back in a
// header: no path, no quotes or control characters, a sane length.
func docName(raw string) (string, error) {
	name := strings.TrimSpace(path.Base(strings.ReplaceAll(raw, `\`, "/")))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return "", errors.New("give the document a name: ?name=<file name>")
	}
	if len(name) > maxDocName {
		return "", fmt.Errorf("document names are at most %d characters", maxDocName)
	}
	return name, nil
}
