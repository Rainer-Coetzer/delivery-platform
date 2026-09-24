// Package api exposes the Order service's REST endpoints, per
// docs/API-CONTRACT.md.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/store"
)

type API struct {
	Pool  *db.Pool
	Store *store.Store
	Log   *slog.Logger
}

func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Route("/orders", func(r chi.Router) {
		r.Post("/", a.createOrder)
		r.Get("/{id}", a.getOrder)
		r.Get("/", a.listOrders)
	})
}

type createOrderRequest struct {
	CustomerID     string             `json:"customerId"`
	CustomerName   string             `json:"customerName"`
	RestaurantID   string             `json:"restaurantId"`
	RestaurantName string             `json:"restaurantName"`
	Items          []events.OrderItem `json:"items"`
	PaymentMethod  string             `json:"paymentMethod"`
	Pickup         events.Location    `json:"pickup"`
	Dropoff        events.Location    `json:"dropoff"`
}

// createOrder computes pricing (including the surge multiplier), persists
// the order and its outbox row in one transaction, and returns 201. The
// outbox relay publishes orders.created shortly after; restaurant and
// payment then race to confirm it, per docs/EVENTS.md.
func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid request body: "+err.Error())
		return
	}
	if req.CustomerID == "" || req.RestaurantID == "" || len(req.Items) == 0 {
		httpx.BadRequest(w, "customerId, restaurantId and at least one item are required")
		return
	}

	var subtotal int64
	for _, item := range req.Items {
		if item.Quantity <= 0 {
			httpx.BadRequest(w, "item quantity must be positive")
			return
		}
		subtotal += item.UnitPriceCents * int64(item.Quantity)
	}

	ctx := r.Context()
	openOrders, err := a.Store.CountOpen(ctx, req.RestaurantID)
	if err != nil {
		a.Log.Error("count open orders", "error", err)
		httpx.Internal(w, "could not price order")
		return
	}
	// Free-driver count would normally come from the delivery service; a
	// fixed estimate keeps the order service independent of a synchronous
	// call to another service on the request path. See docs/ARCHITECTURE.md.
	const assumedFreeDrivers = 5
	surge := domain.SurgeMultiplierFor(openOrders, assumedFreeDrivers)

	const baseDeliveryFeeCents = 1000
	deliveryFee := int64(float64(baseDeliveryFeeCents) * surge)
	total := subtotal + deliveryFee

	order := &domain.Order{
		ID:               uuid.NewString(),
		CustomerID:       req.CustomerID,
		CustomerName:     req.CustomerName,
		RestaurantID:     req.RestaurantID,
		RestaurantName:   req.RestaurantName,
		Status:           events.StatusCreated,
		SubtotalCents:    subtotal,
		DeliveryFeeCents: deliveryFee,
		SurgeMultiplier:  surge,
		TotalCents:       total,
		PaymentMethod:    req.PaymentMethod,
		Pickup:           req.Pickup,
		Dropoff:          req.Dropoff,
	}
	for _, item := range req.Items {
		order.Items = append(order.Items, domain.Item{
			MenuItemID: item.MenuItemID, Name: item.Name,
			Quantity: item.Quantity, UnitPriceCents: item.UnitPriceCents,
		})
	}

	env, err := events.New(events.OrderCreated, order.ID, events.OrderCreatedData{
		OrderID: order.ID, CustomerID: order.CustomerID, CustomerName: order.CustomerName,
		RestaurantID: order.RestaurantID, RestaurantName: order.RestaurantName,
		Items: req.Items, SubtotalCents: subtotal, DeliveryFeeCents: deliveryFee,
		SurgeMultiplier: surge, TotalCents: total, PaymentMethod: req.PaymentMethod,
		Pickup: req.Pickup, Dropoff: req.Dropoff,
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

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	order, err := a.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "order not found")
			return
		}
		a.Log.Error("get order", "error", err)
		httpx.Internal(w, "could not load order")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, order)
}

func (a *API) listOrders(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	var (
		orders []*domain.Order
		err    error
	)
	switch {
	case r.URL.Query().Get("customerId") != "":
		orders, err = a.Store.ListByCustomer(r.Context(), r.URL.Query().Get("customerId"), limit)
	case r.URL.Query().Get("restaurantId") != "":
		orders, err = a.Store.ListByRestaurant(r.Context(), r.URL.Query().Get("restaurantId"), limit)
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
