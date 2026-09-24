// Package api exposes the Order service's REST endpoints.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/catalogue"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/store"
)

// Catalogue is the restaurant lookup createOrder prices from;
// *catalogue.Client in production, a fake in tests.
type Catalogue interface {
	Fetch(ctx context.Context, authorization, restaurantID string) (*catalogue.Restaurant, map[string]domain.CatalogueItem, error)
}

type API struct {
	Pool      *db.Pool
	Store     *store.Store
	Catalogue Catalogue
	Log       *slog.Logger
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
}

func (a *API) Routes(r chi.Router) {
	r.Route("/orders", func(r chi.Router) {
		r.Use(a.Auth)
		r.With(auth.RequireRole(auth.RoleCustomer)).Post("/", a.createOrder)
		r.Get("/{id}", a.getOrder)
		r.Get("/", a.listOrders)
	})
}

type requestedItem struct {
	MenuItemID string `json:"menuItemId"`
	Quantity   int    `json:"quantity"`
	// Name and UnitPriceCents are accepted only so older clients don't get
	// "unknown field" errors. They are ignored: prices and names come from
	// the restaurant's catalogue (audit P0-4).
	Name           string `json:"name,omitempty"`
	UnitPriceCents int64  `json:"unitPriceCents,omitempty"`
}

type createOrderRequest struct {
	RestaurantID  string          `json:"restaurantId"`
	Items         []requestedItem `json:"items"`
	PaymentMethod string          `json:"paymentMethod"`
	Dropoff       events.Location `json:"dropoff"`

	// Ignored, for the same compatibility reason: the customer is whoever
	// the token says, and pickup is the restaurant's own location.
	CustomerID     string          `json:"customerId,omitempty"`
	CustomerName   string          `json:"customerName,omitempty"`
	RestaurantName string          `json:"restaurantName,omitempty"`
	Pickup         events.Location `json:"pickup,omitempty"`
}

// createOrder prices the order from the restaurant's catalogue, persists it
// with its outbox row in one transaction, and returns 201. The outbox relay
// then publishes orders.created, and restaurant and payment race to confirm
// it (topics in pkg/events).
func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	caller := auth.MustFromContext(r.Context())
	var req createOrderRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid request body: "+err.Error())
		return
	}
	if req.RestaurantID == "" || req.PaymentMethod == "" {
		httpx.BadRequest(w, "restaurantId and paymentMethod are required")
		return
	}
	if !validDropoff(req.Dropoff) {
		httpx.BadRequest(w, "dropoff must be a real coordinate (lat -90..90, lng -180..180)")
		return
	}

	ctx := r.Context()
	rest, menu, err := a.Catalogue.Fetch(ctx, r.Header.Get("Authorization"), req.RestaurantID)
	if errors.Is(err, catalogue.ErrRestaurantNotFound) {
		httpx.BadRequest(w, "restaurant not found")
		return
	}
	if err != nil {
		a.Log.Error("fetch catalogue", "restaurantId", req.RestaurantID, "error", err)
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "could not load the restaurant's menu, try again")
		return
	}

	lines := make([]domain.RequestedLine, 0, len(req.Items))
	for _, it := range req.Items {
		lines = append(lines, domain.RequestedLine{MenuItemID: it.MenuItemID, Quantity: it.Quantity})
	}
	items, subtotal, err := domain.PriceItems(lines, menu)
	if err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}

	openOrders, err := a.Store.CountOpen(ctx, req.RestaurantID)
	if err != nil {
		a.Log.Error("count open orders", "error", err)
		httpx.Internal(w, "could not price order")
		return
	}
	// Known debt (audit P1, Phase 4): surge uses one restaurant's load and a
	// fixed driver estimate, and the fee below is float maths. Both move to
	// pricing.Policy.
	const assumedFreeDrivers = 5
	surge := domain.SurgeMultiplierFor(openOrders, assumedFreeDrivers)
	const baseDeliveryFeeCents = 1000
	deliveryFee := int64(float64(baseDeliveryFeeCents) * surge)
	total := subtotal + deliveryFee

	pickup := events.Location{Lat: rest.Lat, Lng: rest.Lng, Label: rest.Name}
	order := &domain.Order{
		ID:               uuid.NewString(),
		CustomerID:       caller.UserID,
		CustomerName:     caller.Name,
		RestaurantID:     rest.ID,
		RestaurantName:   rest.Name,
		Status:           events.StatusCreated,
		Items:            items,
		SubtotalCents:    subtotal,
		DeliveryFeeCents: deliveryFee,
		SurgeMultiplier:  surge,
		TotalCents:       total,
		PaymentMethod:    req.PaymentMethod,
		Pickup:           pickup,
		Dropoff:          req.Dropoff,
	}

	eventItems := make([]events.OrderItem, 0, len(items))
	for _, it := range items {
		eventItems = append(eventItems, events.OrderItem{MenuItemID: it.MenuItemID, Name: it.Name, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
	}
	env, err := events.New(events.OrderCreated, order.ID, events.OrderCreatedData{
		OrderID: order.ID, CustomerID: order.CustomerID, CustomerName: order.CustomerName,
		RestaurantID: order.RestaurantID, RestaurantName: order.RestaurantName,
		Items: eventItems, SubtotalCents: subtotal, DeliveryFeeCents: deliveryFee,
		SurgeMultiplier: surge, TotalCents: total, PaymentMethod: req.PaymentMethod,
		Pickup: pickup, Dropoff: req.Dropoff,
	})
	if err != nil {
		httpx.Internal(w, "could not build event")
		return
	}

	err = a.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		if err := store.Create(ctx, tx, order); err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, env)
	})
	if err != nil {
		a.Log.Error("create order", "error", err)
		httpx.Internal(w, "could not create order")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, order)
}

// validDropoff rejects out-of-range coordinates and the 0,0 an omitted
// dropoff decodes to (a point in the Atlantic that would send a driver
// nowhere useful).
func validDropoff(l events.Location) bool {
	if l.Lat == 0 && l.Lng == 0 {
		return false
	}
	return l.Lat >= -90 && l.Lat <= 90 && l.Lng >= -180 && l.Lng <= 180
}

// CanView reports whether caller may see order o: its customer, its
// restaurant, its assigned driver, or an admin. The gateway's WebSocket
// relies on this too: it asks GET /orders/{id} as the user before letting
// them watch an order live.
func CanView(caller *auth.Claims, o *domain.Order) bool {
	switch {
	case caller.IsAdmin():
		return true
	case caller.Role == auth.RoleCustomer:
		return caller.UserID == o.CustomerID
	case caller.Role == auth.RoleRestaurant:
		return caller.ActsAs(auth.RoleRestaurant, o.RestaurantID)
	case caller.Role == auth.RoleDriver:
		return o.DriverID != "" && caller.ActsAs(auth.RoleDriver, o.DriverID)
	}
	return false
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	order, err := a.Store.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "order not found")
			return
		}
		a.Log.Error("get order", "error", err)
		httpx.Internal(w, "could not load order")
		return
	}
	if !CanView(auth.MustFromContext(r.Context()), order) {
		httpx.Forbidden(w, "this isn't your order")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, order)
}

// listOrders: customers always get their own orders (a customerId param for
// anyone else is refused, not silently replaced), restaurants only their
// own restaurantId, admins anything.
func (a *API) listOrders(w http.ResponseWriter, r *http.Request) {
	caller := auth.MustFromContext(r.Context())
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	customerID := r.URL.Query().Get("customerId")
	restaurantID := r.URL.Query().Get("restaurantId")

	if caller.Role == auth.RoleCustomer {
		if customerID != "" && customerID != caller.UserID {
			httpx.Forbidden(w, "you can only list your own orders")
			return
		}
		customerID, restaurantID = caller.UserID, ""
	}
	if restaurantID != "" && !caller.ActsAs(auth.RoleRestaurant, restaurantID) {
		httpx.Forbidden(w, "you can only list your own restaurant's orders")
		return
	}
	if customerID != "" && !caller.IsAdmin() && customerID != caller.UserID {
		httpx.Forbidden(w, "you can only list your own orders")
		return
	}

	var (
		orders []*domain.Order
		err    error
	)
	switch {
	case customerID != "":
		orders, err = a.Store.ListByCustomer(r.Context(), customerID, limit)
	case restaurantID != "":
		orders, err = a.Store.ListByRestaurant(r.Context(), restaurantID, limit)
	default:
		httpx.BadRequest(w, "customerId or restaurantId query parameter is required")
		return
	}
	if err != nil {
		a.Log.Error("list orders", "error", err)
		httpx.Internal(w, "could not list orders")
		return
	}
	if orders == nil {
		orders = []*domain.Order{}
	}
	httpx.WriteJSON(w, http.StatusOK, orders)
}
