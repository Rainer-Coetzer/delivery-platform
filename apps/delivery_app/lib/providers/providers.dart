// Central Riverpod wiring: one ApiClient, one AuthController holding the
// logged-in user, and small repository classes for each backend service the
// app talks to (all through the gateway).
import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../core/api_client.dart';
import '../models/models.dart';

final apiClientProvider = Provider<ApiClient>((ref) => ApiClient());

// --- Auth ---

class AuthState {
  final User? user;
  final bool loading;
  const AuthState({this.user, this.loading = false});
  bool get isLoggedIn => user != null;
}

class AuthController extends StateNotifier<AuthState> {
  final ApiClient _api;
  AuthController(this._api) : super(const AuthState());

  Future<String?> login(String email, String password) async {
    state = const AuthState(loading: true);
    try {
      final res = await _api.dio.post('/auth/login', data: {'email': email, 'password': password});
      final token = res.data['token'] as String;
      final user = User.fromJson(res.data['user'] as Map<String, dynamic>);
      await _api.saveToken(token);
      state = AuthState(user: user);
      return null;
    } on DioException catch (e) {
      state = const AuthState();
      return _errorMessage(e);
    }
  }

  /// Public sign-up always creates a customer. Restaurant, driver and admin
  /// accounts are created by an admin (POST /users), already linked to
  /// their restaurant or driver record.
  Future<String?> register(String name, String email, String password) async {
    state = const AuthState(loading: true);
    try {
      await _api.dio.post('/auth/register', data: {
        'name': name, 'email': email, 'password': password,
      });
      state = const AuthState();
      return null;
    } on DioException catch (e) {
      state = const AuthState();
      return _errorMessage(e);
    }
  }

  Future<void> logout() async {
    await _api.clearToken();
    state = const AuthState();
  }

  String _errorMessage(DioException e) {
    final data = e.response?.data;
    if (data is Map && data['message'] != null) return data['message'] as String;
    return e.message ?? 'Something went wrong';
  }
}

final authControllerProvider = StateNotifierProvider<AuthController, AuthState>((ref) {
  return AuthController(ref.watch(apiClientProvider));
});

// --- Restaurants & menu ---

class RestaurantRepository {
  final ApiClient _api;
  RestaurantRepository(this._api);

  Future<List<Restaurant>> list() async {
    final res = await _api.dio.get('/merchants');
    return (res.data as List).map((e) => Restaurant.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<List<MenuItem>> menu(String restaurantId) async {
    final res = await _api.dio.get('/merchants/$restaurantId/catalogue-items');
    return (res.data as List).map((e) => MenuItem.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<void> createMenuItem(String restaurantId, {required String name, required int priceCents, required int stockQty}) {
    return _api.dio.post('/merchants/$restaurantId/catalogue-items', data: {
      'name': name, 'priceCents': priceCents, 'stockQty': stockQty, 'available': true,
    });
  }

  Future<void> updateMenuItem(String restaurantId, MenuItem item) {
    return _api.dio.patch('/merchants/$restaurantId/catalogue-items/${item.id}', data: {
      'name': item.name, 'priceCents': item.priceCents, 'stockQty': item.stockQty, 'available': item.available,
    });
  }

  Future<void> markReady(String restaurantId, String orderId) {
    return _api.dio.post('/merchants/$restaurantId/jobs/$orderId/ready');
  }
}

final restaurantRepositoryProvider = Provider((ref) => RestaurantRepository(ref.watch(apiClientProvider)));

final restaurantsListProvider = FutureProvider<List<Restaurant>>((ref) {
  return ref.watch(restaurantRepositoryProvider).list();
});

final menuProvider = FutureProvider.family<List<MenuItem>, String>((ref, restaurantId) {
  return ref.watch(restaurantRepositoryProvider).menu(restaurantId);
});

// --- Cart (customer-side, in-memory) ---

class CartController extends StateNotifier<Map<String, CartLine>> {
  CartController() : super({});

  /// The restaurant the current cart belongs to (a cart only ever holds
  /// items from one restaurant at a time — adding from a different one
  /// starts a fresh cart, matching how most delivery apps behave).
  String? restaurantId;
  String? restaurantName;

  void add(MenuItem item, {required String restaurantName}) {
    if (restaurantId != null && restaurantId != item.restaurantId) {
      state = {};
    }
    restaurantId = item.restaurantId;
    this.restaurantName = restaurantName;
    final existing = state[item.id];
    state = {
      ...state,
      item.id: existing != null ? (existing..quantity += 1) : CartLine(item: item),
    };
  }

  void remove(String menuItemId) {
    final next = {...state};
    final line = next[menuItemId];
    if (line == null) return;
    if (line.quantity <= 1) {
      next.remove(menuItemId);
    } else {
      line.quantity--;
    }
    state = next;
  }

  void clear() {
    state = {};
    restaurantId = null;
    restaurantName = null;
  }

  int get totalCents => state.values.fold(0, (sum, l) => sum + l.subtotalCents);
}

final cartProvider = StateNotifierProvider<CartController, Map<String, CartLine>>((ref) => CartController());

// --- Orders ---

class OrderRepository {
  final ApiClient _api;
  OrderRepository(this._api);

  /// Sends only what the customer chooses. The server prices every line
  /// from the restaurant's own menu, takes the customer from the token and
  /// the pickup point from the restaurant, so none of that is sent.
  Future<Order> create({
    required String restaurantId,
    required List<CartLine> lines,
    required String paymentMethod,
    required LatLng2 dropoff,
  }) async {
    final res = await _api.dio.post('/jobs/', data: {
      'merchantId': restaurantId,
      'items': lines.map((l) => {'catalogueItemId': l.item.id, 'quantity': l.quantity}).toList(),
      'paymentMethod': paymentMethod,
      'dropoff': dropoff.toJson(),
    });
    return Order.fromJson(res.data as Map<String, dynamic>);
  }

  Future<Order> get(String id) async {
    final res = await _api.dio.get('/jobs/$id');
    return Order.fromJson(res.data as Map<String, dynamic>);
  }

  Future<List<Order>> byCustomer(String customerId) async {
    final res = await _api.dio.get('/jobs/', queryParameters: {'customerId': customerId});
    return (res.data as List).map((e) => Order.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<List<Order>> byRestaurant(String restaurantId) async {
    final res = await _api.dio.get('/jobs/', queryParameters: {'merchantId': restaurantId});
    return (res.data as List).map((e) => Order.fromJson(e as Map<String, dynamic>)).toList();
  }
}

final orderRepositoryProvider = Provider((ref) => OrderRepository(ref.watch(apiClientProvider)));

final restaurantOrdersProvider = FutureProvider.family<List<Order>, String>((ref, restaurantId) {
  return ref.watch(orderRepositoryProvider).byRestaurant(restaurantId);
});

// --- Live order tracking over WebSocket ---

/// Emits every raw event envelope the gateway forwards for one order:
/// jobs.status-changed and dispatch.location. The UI decodes `eventType`
/// to decide what changed.
final orderEventsProvider = StreamProvider.family.autoDispose<Map<String, dynamic>, String>((ref, orderId) async* {
  final api = ref.watch(apiClientProvider);
  // The gateway requires the JWT on the socket (browsers can't set headers
  // on a WebSocket handshake, so it goes in the query string).
  final token = await api.readToken() ?? '';
  final channel = WebSocketChannel.connect(Uri.parse(api.wsUrlForOrder(orderId, token)));
  ref.onDispose(() => channel.sink.close());
  yield* channel.stream.map((raw) => jsonDecode(raw as String) as Map<String, dynamic>);
});

// --- Delivery / drivers ---

class DeliveryRepository {
  final ApiClient _api;
  DeliveryRepository(this._api);

  Future<List<Driver>> listDrivers() async {
    final res = await _api.dio.get('/agents');
    return (res.data as List).map((e) => Driver.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<void> setStatus(String driverId, String status) {
    return _api.dio.patch('/agents/$driverId/status', data: {'status': status});
  }

  Future<void> reportLocation(String driverId, double lat, double lng) {
    return _api.dio.patch('/agents/$driverId/location', data: {'lat': lat, 'lng': lng});
  }

  Future<void> pickedUp(String deliveryId) => _api.dio.post('/assignments/$deliveryId/picked-up');
  Future<void> completed(String deliveryId) => _api.dio.post('/assignments/$deliveryId/completed');
}

final deliveryRepositoryProvider = Provider((ref) => DeliveryRepository(ref.watch(apiClientProvider)));

// --- Notifications ---

class NotificationRepository {
  final ApiClient _api;
  NotificationRepository(this._api);

  Future<List<NotificationDto>> listFor(String recipient) async {
    final res = await _api.dio.get('/notifications', queryParameters: {'recipient': recipient});
    return (res.data as List).map((e) => NotificationDto.fromJson(e as Map<String, dynamic>)).toList();
  }
}

final notificationRepositoryProvider = Provider((ref) => NotificationRepository(ref.watch(apiClientProvider)));

// --- Admin ---

class AdminRepository {
  final ApiClient _api;
  AdminRepository(this._api);

  Future<Map<String, dynamic>> overview() async {
    final res = await _api.dio.get('/admin/overview');
    return res.data as Map<String, dynamic>;
  }
}

final adminRepositoryProvider = Provider((ref) => AdminRepository(ref.watch(apiClientProvider)));
