package ws

import (
	"context"
	"net/http"
	"net/url"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

// OrderAccess decides who may watch an order by asking the Order service
// GET /orders/{id} *as the user*. That endpoint already enforces who may see
// an order (customer, its restaurant, its driver, admin: order api.CanView),
// so the rule lives in exactly one place instead of being copied here.
type OrderAccess struct {
	OrderServiceURL string
	Issuer          *auth.Issuer
	HTTP            *http.Client
}

func NewOrderAccess(orderServiceURL string, client *http.Client, issuer *auth.Issuer) *OrderAccess {
	return &OrderAccess{OrderServiceURL: orderServiceURL, Issuer: issuer, HTTP: client}
}

// Authorize matches Hub.Authorize. A bad token is rejected locally before
// any network call.
func (o *OrderAccess) Authorize(ctx context.Context, token, orderID string) int {
	if _, err := o.Issuer.Verify(token); err != nil {
		return http.StatusUnauthorized
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.OrderServiceURL+"/orders/"+url.PathEscape(orderID), nil)
	if err != nil {
		return http.StatusBadRequest // orderID made an invalid URL
	}
	// PathEscape: orderId is client input. Unescaped, "x/../y" would ask the
	// order service a different question than the room the client joins.
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return http.StatusBadGateway
	}
	resp.Body.Close()
	return resp.StatusCode
}
