package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

type manualAssign struct {
	JobID   string `json:"jobId"`
	AgentID string `json:"agentId"`
}

// assignByHand gives a queued job to an agent an admin chose: every job
// under the manual strategy, and a way out when a job waits too long under
// any other.
func (a *API) assignByHand(w http.ResponseWriter, r *http.Request) {
	var req manualAssign
	if err := httpx.Decode(r, &req); err != nil || req.JobID == "" || req.AgentID == "" {
		httpx.BadRequest(w, `body must be {"jobId": "...", "agentId": "..."}`)
		return
	}
	a.takeQueued(w, r, req.JobID, req.AgentID, nil)
}

// acceptOffer: the agent who accepts an open offer first gets the job.
func (a *API) acceptOffer(w http.ResponseWriter, r *http.Request) {
	caller := auth.MustFromContext(r.Context())
	if caller.Role != auth.RoleAgent || caller.RefID == "" {
		httpx.Forbidden(w, "only an agent can accept an offer")
		return
	}
	jobID := chi.URLParam(r, "jobId")
	a.takeQueued(w, r, jobID, caller.RefID, func(tx pgx.Tx) error {
		ok, err := store.HasOpenOffer(r.Context(), tx, jobID, caller.RefID)
		if err == nil && !ok {
			err = errNoOffer
		}
		return err
	})
}

var (
	errNoOffer   = errors.New("no open offer of this job to you")
	errCancelled = errors.New("job was cancelled")
)

// takeQueued assigns a queued job to agentID, after check (if any), with
// the job locked against the retrier, a cancellation, and a second agent
// accepting the same offer.
func (a *API) takeQueued(w http.ResponseWriter, r *http.Request, jobID, agentID string, check func(pgx.Tx) error) {
	ctx := r.Context()
	err := a.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		if err := store.LockJob(ctx, tx, jobID); err != nil {
			return err
		}
		if cancelled, err := store.IsJobCancelled(ctx, tx, jobID); err != nil || cancelled {
			if err == nil {
				err = errCancelled
			}
			return err
		}
		route, err := store.GetPendingForUpdate(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if check != nil {
			if err := check(tx); err != nil {
				return err
			}
		}
		return a.Dispatcher.AssignAgent(ctx, tx, route, agentID)
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Conflict(w, "that job isn't waiting for an agent (already assigned, or never queued)")
	case errors.Is(err, errCancelled):
		httpx.Conflict(w, "that job was cancelled")
	case errors.Is(err, errNoOffer):
		httpx.Conflict(w, "the offer has expired or went to someone else")
	case errors.Is(err, consumers.ErrAgentNotAvailable):
		httpx.Conflict(w, "the agent isn't available (busy, offline, or just taken)")
	case errors.Is(err, consumers.ErrAgentDoesNotFit):
		httpx.Conflict(w, err.Error())
	case err != nil:
		a.Log.Error("assign queued job", "jobId", jobID, "agentId", agentID, "error", err)
		httpx.Internal(w, "could not assign the job")
	default:
		d, err := a.Store.GetByJob(ctx, jobID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"jobId": jobID, "agentId": agentID})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, d)
	}
}

// listQueue is every job waiting for an agent, due or not.
func (a *API) listQueue(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListAllPending(r.Context())
	if err != nil {
		a.Log.Error("list queue", "error", err)
		httpx.Internal(w, "could not list the queue")
		return
	}
	if list == nil {
		list = []*store.Route{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// listOffers is an agent's open offers.
func (a *API) listOffers(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.OffersFor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.Log.Error("list offers", "error", err)
		httpx.Internal(w, "could not list offers")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}
