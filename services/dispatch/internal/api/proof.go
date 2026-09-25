package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/blob"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

// Proof kinds a stop can ask for, and the two that stand in for them.
const (
	proofPhoto     = "photo"
	proofSignature = "signature"
	proofPIN       = "pin"
	proofIDCheck   = "id_check"
	proofOverride  = "admin_override"
)

// maxProofJSON bounds the small JSON proofs (PIN, ID check, override).
// Uploads are bounded by the pack's settings instead.
const maxProofJSON = 16 << 10

var imageTypes = map[string]string{"image/jpeg": "jpg", "image/png": "png"}

type pinProof struct {
	PIN string `json:"pin"`
}

// idCheck is the agent's attestation: what they saw, not a copy of the
// document. Storing ID numbers in full is a liability nobody asked for.
type idCheck struct {
	IDType     string `json:"idType"`     // e.g. national_id, passport, drivers_licence
	Last4      string `json:"last4"`      // last four characters of the document number
	HolderName string `json:"holderName"` // as printed
}

type override struct {
	Reason string `json:"override"`
}

// submitProof takes the proof the next stop on the route needs, from the
// assigned agent. An admin may instead record an override with a reason
// (a locked PIN, a broken camera), which is kept and shown as such.
func (a *API) submitProof(w http.ResponseWriter, r *http.Request) {
	if a.Proof == nil {
		httpx.NotFound(w, "this platform doesn't take proof")
		return
	}
	ctx := r.Context()
	caller := auth.MustFromContext(ctx)
	d, seq, ok := a.loadStop(w, r, assignedTo)
	if !ok {
		return
	}
	if d.Next == nil || d.Next.Seq != seq {
		httpx.Conflict(w, "proof goes with the next open stop on the route")
		return
	}
	need := d.Next.Proof
	if need == "" {
		httpx.Conflict(w, "stop "+strconv.Itoa(seq)+" needs no proof")
		return
	}
	p := &store.Proof{ID: uuid.NewString(), AssignmentID: d.ID, Seq: seq, Kind: need, SubmittedBy: caller.UserID}

	if caller.IsAdmin() && isJSON(r) {
		var o override
		if err := decodeSmall(w, r, &o); err != nil || strings.TrimSpace(o.Reason) == "" {
			httpx.BadRequest(w, `an admin records {"override": "<why>"} instead of the proof`)
			return
		}
		p.Kind = proofOverride
		p.Details, _ = json.Marshal(map[string]string{"reason": o.Reason, "stoodInFor": need})
		a.saveProof(w, r, p)
		return
	}

	switch need {
	case proofPhoto, proofSignature:
		ext, ok := imageTypes[mediaType(r)]
		if !ok {
			httpx.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "send the "+need+" as image/jpeg or image/png")
			return
		}
		p.BlobKey = fmt.Sprintf("proof/%s/%d-%s.%s", strings.ToLower(d.ID), seq, p.ID, ext)
		p.ContentType = mediaType(r)
		// Stored before the row: if the row then fails, an unreferenced
		// file is left, which is cheaper than a proof row with no file.
		n, err := a.Blobs.Put(ctx, p.BlobKey, r.Body, a.Proof.MaxPhotoBytes)
		if errors.Is(err, blob.ErrTooLarge) {
			httpx.WriteError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("the %s must be at most %d bytes", need, a.Proof.MaxPhotoBytes))
			return
		}
		if err != nil {
			a.Log.Error("store proof", "assignmentId", d.ID, "seq", seq, "error", err)
			httpx.Internal(w, "could not store the "+need)
			return
		}
		p.SizeBytes = n
		a.saveProof(w, r, p)
	case proofPIN:
		var body pinProof
		if err := decodeSmall(w, r, &body); err != nil || body.PIN == "" {
			httpx.BadRequest(w, `send {"pin": "<the code the customer gives you>"}`)
			return
		}
		a.checkPIN(w, r, d, seq, body.PIN, p)
	case proofIDCheck:
		var body idCheck
		if err := decodeSmall(w, r, &body); err != nil || body.IDType == "" || strings.TrimSpace(body.HolderName) == "" || len(body.Last4) != 4 {
			httpx.BadRequest(w, `send {"idType": "...", "last4": "<last 4 characters of the ID number>", "holderName": "..."}`)
			return
		}
		p.Details, _ = json.Marshal(body)
		a.saveProof(w, r, p)
	default:
		a.Log.Error("stop asks for an unknown proof kind", "assignmentId", d.ID, "seq", seq, "proof", need)
		httpx.Internal(w, "this stop asks for proof the platform doesn't know: "+need)
	}
}

// checkPIN compares the code with the stop's hash. A wrong code counts
// against the stop even though the request fails (it commits on its own),
// and after settings.proof.pin_attempts wrong codes the stop locks until
// an admin overrides: four digits are only safe behind a limit.
func (a *API) checkPIN(w http.ResponseWriter, r *http.Request, d *store.Assignment, seq int, pin string, p *store.Proof) {
	ctx := r.Context()
	var attempts int
	var matched bool
	err := a.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		// Locked: without it, a burst of parallel guesses all read the
		// count before any of them adds to it, and the limit means nothing.
		hash, tries, err := store.LockPIN(ctx, tx, d.ID, seq)
		if err != nil {
			return err
		}
		if hash == "" {
			return fmt.Errorf("stop %d of assignment %s wants a PIN but has no PIN hash", seq, d.ID)
		}
		if attempts = tries; attempts >= a.Proof.PinAttempts {
			return nil
		}
		matched = subtle.ConstantTimeCompare([]byte(events.PinHash(d.JobID, seq, pin)), []byte(hash)) == 1
		if !matched {
			attempts, err = store.CountWrongPIN(ctx, tx, d.ID, seq)
		}
		return err
	})
	switch {
	case err != nil:
		a.Log.Error("check PIN", "assignmentId", d.ID, "seq", seq, "error", err)
		httpx.Internal(w, "could not check the PIN")
	case !matched && attempts >= a.Proof.PinAttempts:
		httpx.WriteError(w, http.StatusLocked, "pin_locked", "too many wrong PINs: this stop needs an admin override now")
	case !matched:
		httpx.WriteError(w, http.StatusUnprocessableEntity, "wrong_pin",
			fmt.Sprintf("that PIN doesn't match; %d tries left", a.Proof.PinAttempts-attempts))
	default:
		p.Details = json.RawMessage(`{"matched": true}`)
		a.saveProof(w, r, p)
	}
}

func (a *API) saveProof(w http.ResponseWriter, r *http.Request, p *store.Proof) {
	err := a.Pool.WithTx(r.Context(), func(tx pgx.Tx) error { return store.InsertProof(r.Context(), tx, p) })
	switch {
	case errors.Is(err, store.ErrProofExists):
		httpx.Conflict(w, "this stop already has its proof")
	case err != nil:
		a.Log.Error("save proof", "assignmentId", p.AssignmentID, "seq", p.Seq, "error", err)
		httpx.Internal(w, "could not save the proof")
	default:
		httpx.WriteJSON(w, http.StatusCreated, p)
	}
}

// getProof shows the proof captured at a stop to the people it concerns:
// the agent, the job's customer and merchant, admins. A photo or signature
// comes back as the image itself; anything else as the record, never a PIN.
func (a *API) getProof(w http.ResponseWriter, r *http.Request) {
	if a.Proof == nil {
		httpx.NotFound(w, "this platform doesn't take proof")
		return
	}
	d, seq, ok := a.loadStop(w, r, func(c *auth.Claims, d *store.Assignment) bool {
		return assignedTo(c, d) ||
			(c.Role == auth.RoleCustomer && d.CustomerID != "" && c.UserID == d.CustomerID) ||
			(d.MerchantID != "" && c.ActsAs(auth.RoleMerchant, d.MerchantID))
	})
	if !ok {
		return
	}
	p, err := store.GetProof(r.Context(), a.Pool, d.ID, seq)
	if errors.Is(err, store.ErrNotFound) {
		httpx.NotFound(w, "no proof at this stop")
		return
	}
	if err != nil {
		a.Log.Error("get proof", "error", err)
		httpx.Internal(w, "could not load the proof")
		return
	}
	if p.BlobKey == "" {
		httpx.WriteJSON(w, http.StatusOK, p)
		return
	}
	f, err := a.Blobs.Open(r.Context(), p.BlobKey)
	if err != nil {
		a.Log.Error("open proof file", "key", p.BlobKey, "error", err)
		httpx.Internal(w, "could not load the proof")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// loadStop resolves {id} and {seq} for a caller that may see the
// assignment (else 404, as for one that doesn't exist).
func (a *API) loadStop(w http.ResponseWriter, r *http.Request, may func(*auth.Claims, *store.Assignment) bool) (*store.Assignment, int, bool) {
	seq, err := strconv.Atoi(chi.URLParam(r, "seq"))
	if err != nil {
		httpx.BadRequest(w, "stop number must be an integer")
		return nil, 0, false
	}
	d, err := a.Store.Get(r.Context(), chi.URLParam(r, "id"))
	if err == nil && !may(auth.MustFromContext(r.Context()), d) {
		err = store.ErrNotFound
	}
	if errors.Is(err, store.ErrNotFound) {
		httpx.NotFound(w, "assignment not found")
		return nil, 0, false
	}
	if err != nil {
		a.Log.Error("load assignment", "error", err)
		httpx.Internal(w, "could not load assignment")
		return nil, 0, false
	}
	return d, seq, true
}

func mediaType(r *http.Request) string {
	t, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	return strings.ToLower(strings.TrimSpace(t))
}

func isJSON(r *http.Request) bool { return mediaType(r) == "application/json" }

// decodeSmall reads a small JSON proof. Upload routes skip the global body
// limit (pkg/app), so this one sets its own.
func decodeSmall(w http.ResponseWriter, r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxProofJSON)
	return httpx.Decode(r, into)
}
