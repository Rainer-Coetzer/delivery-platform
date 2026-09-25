package ws

import (
	"context"
	"net/http"
	"net/url"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

// JobAccess decides who may watch a job by asking the jobs module
// GET /jobs/{id} *as the user*. That endpoint already enforces who may see
// a job (customer, its merchant, its agent, admin: job api.CanView),
// so the rule lives in exactly one place instead of being copied here.
type JobAccess struct {
	JobServiceURL string
	Issuer        *auth.Issuer
	HTTP          *http.Client
}

func NewJobAccess(jobsServiceURL string, client *http.Client, issuer *auth.Issuer) *JobAccess {
	return &JobAccess{JobServiceURL: jobsServiceURL, Issuer: issuer, HTTP: client}
}

// Authorize matches Hub.Authorize. A bad token is rejected locally before
// any network call.
func (o *JobAccess) Authorize(ctx context.Context, token, jobID string) int {
	if _, err := o.Issuer.Verify(token); err != nil {
		return http.StatusUnauthorized
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.JobServiceURL+"/jobs/"+url.PathEscape(jobID), nil)
	if err != nil {
		return http.StatusBadRequest // jobID made an invalid URL
	}
	// PathEscape: jobId is client input. Unescaped, "x/../y" would ask the
	// job service a different question than the room the client joins.
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return http.StatusBadGateway
	}
	resp.Body.Close()
	return resp.StatusCode
}
