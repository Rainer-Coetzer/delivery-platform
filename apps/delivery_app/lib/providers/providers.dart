// Central Riverpod wiring: one ApiClient, the platform config, one
// AuthController holding the logged-in user, and small repository classes
// for each backend module the app talks to (all through the gateway).
import 'dart:async';
import 'dart:convert';
import 'dart:ui' show PlatformDispatcher;

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../core/api_client.dart';
import '../core/config.dart';
import '../models/models.dart';

final apiClientProvider = Provider<ApiClient>((ref) => ApiClient());

/// The platform's configuration (GET /config): brand, words, lifecycle,
/// modules. Fetched once; it only changes when the server's brand or pack
/// does. Asked for in the device's language: the server answers in it if
/// the brand supports it, else in the brand's default, and says which
/// (config.locale). The app's own strings follow that choice, so the
/// pack's words and the app's never come in two languages.
final configProvider = FutureProvider<AppConfig>((ref) async {
  final device = PlatformDispatcher.instance.locale.languageCode;
  final res = await ref.watch(apiClientProvider).dio.get('/config', queryParameters: {'locale': device});
  return AppConfig.fromJson(res.data as Map<String, dynamic>);
});

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

  /// Public sign-up always creates a customer. Merchant, agent and admin
  /// accounts are created by an admin (POST /users), already linked to
  /// their merchant or agent record.
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
    // '' when there's nothing to show: the screen says it in the user's
    // language (the server's messages are English; see PROGRESS, Later).
    return e.message ?? '';
  }
}

final authControllerProvider = StateNotifierProvider<AuthController, AuthState>((ref) {
  return AuthController(ref.watch(apiClientProvider));
});

// --- Merchants & catalogue (catalogue module) ---

class MerchantRepository {
  final ApiClient _api;
  MerchantRepository(this._api);

  Future<List<Merchant>> list() async {
    final res = await _api.dio.get('/merchants');
    return (res.data as List).map((e) => Merchant.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<List<CatalogueItem>> catalogue(String merchantId) async {
    final res = await _api.dio.get('/merchants/$merchantId/catalogue-items');
    return (res.data as List).map((e) => CatalogueItem.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<void> createItem(String merchantId, {required String name, required int priceCents, required int stockQty}) {
    return _api.dio.post('/merchants/$merchantId/catalogue-items', data: {
      'name': name, 'priceCents': priceCents, 'stockQty': stockQty, 'available': true,
    });
  }

  Future<void> updateItem(String merchantId, CatalogueItem item) {
    return _api.dio.patch('/merchants/$merchantId/catalogue-items/${item.id}', data: {
      'name': item.name, 'priceCents': item.priceCents, 'stockQty': item.stockQty, 'available': item.available,
    });
  }

  Future<void> markReady(String merchantId, String jobId) {
    return _api.dio.post('/merchants/$merchantId/jobs/$jobId/ready');
  }
}

final merchantRepositoryProvider = Provider((ref) => MerchantRepository(ref.watch(apiClientProvider)));

final merchantsListProvider = FutureProvider<List<Merchant>>((ref) {
  return ref.watch(merchantRepositoryProvider).list();
});

final catalogueProvider = FutureProvider.family<List<CatalogueItem>, String>((ref, merchantId) {
  return ref.watch(merchantRepositoryProvider).catalogue(merchantId);
});

// --- Cart (customer-side, in-memory) ---

class CartController extends StateNotifier<Map<String, CartLine>> {
  CartController() : super({});

  /// The merchant the current cart belongs to (a cart only ever holds items
  /// from one merchant at a time: adding from another starts a fresh cart,
  /// as most ordering apps do).
  String? merchantId;
  String? merchantName;

  void add(CatalogueItem item, {required String merchantName}) {
    if (merchantId != null && merchantId != item.merchantId) {
      state = {};
    }
    merchantId = item.merchantId;
    this.merchantName = merchantName;
    final existing = state[item.id];
    state = {
      ...state,
      item.id: existing != null ? (existing..quantity += 1) : CartLine(item: item),
    };
  }

  void remove(String itemId) {
    final next = {...state};
    final line = next[itemId];
    if (line == null) return;
    if (line.quantity <= 1) {
      next.remove(itemId);
    } else {
      line.quantity--;
    }
    state = next;
  }

  void clear() {
    state = {};
    merchantId = null;
    merchantName = null;
  }

  int get totalCents => state.values.fold(0, (sum, l) => sum + l.subtotalCents);
}

final cartProvider = StateNotifierProvider<CartController, Map<String, CartLine>>((ref) => CartController());

// --- Jobs ---

class JobRepository {
  final ApiClient _api;
  JobRepository(this._api);

  /// Sends only what the customer chooses. The server prices every line
  /// from the merchant's own catalogue, takes the customer from the token
  /// and the pickup point from the merchant, so none of that is sent.
  Future<Job> createFromCatalogue({
    required String merchantId,
    required List<CartLine> lines,
    required String paymentMethod,
    required LatLng2 dropoff,
  }) async {
    final res = await _api.dio.post('/jobs/', data: {
      'merchantId': merchantId,
      'items': lines.map((l) => {'catalogueItemId': l.item.id, 'quantity': l.quantity}).toList(),
      'paymentMethod': paymentMethod,
      'dropoff': dropoff.toJson(),
    });
    return Job.fromJson(res.data as Map<String, dynamic>);
  }

  Future<Job> get(String id) async {
    final res = await _api.dio.get('/jobs/$id');
    return Job.fromJson(res.data as Map<String, dynamic>);
  }

  Future<List<Job>> byCustomer(String customerId) async {
    final res = await _api.dio.get('/jobs/', queryParameters: {'customerId': customerId});
    return (res.data as List).map((e) => Job.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<List<Job>> byMerchant(String merchantId) async {
    final res = await _api.dio.get('/jobs/', queryParameters: {'merchantId': merchantId});
    return (res.data as List).map((e) => Job.fromJson(e as Map<String, dynamic>)).toList();
  }
}

final jobRepositoryProvider = Provider((ref) => JobRepository(ref.watch(apiClientProvider)));

final merchantJobsProvider = FutureProvider.family<List<Job>, String>((ref, merchantId) {
  return ref.watch(jobRepositoryProvider).byMerchant(merchantId);
});

// --- Live job tracking over WebSocket ---

/// Emits every raw event envelope the gateway forwards for one job:
/// jobs.status-changed and dispatch.location. The UI decodes `eventType`
/// to decide what changed.
final jobEventsProvider = StreamProvider.family.autoDispose<Map<String, dynamic>, String>((ref, jobId) async* {
  final api = ref.watch(apiClientProvider);
  // The gateway requires the JWT on the socket (browsers can't set headers
  // on a WebSocket handshake, so it goes in the query string).
  final token = await api.readToken() ?? '';
  final channel = WebSocketChannel.connect(Uri.parse(api.wsUrlForJob(jobId, token)));
  ref.onDispose(() => channel.sink.close());
  yield* channel.stream.map((raw) => jsonDecode(raw as String) as Map<String, dynamic>);
});

// --- Dispatch: agents and assignments ---

class DispatchRepository {
  final ApiClient _api;
  DispatchRepository(this._api);

  Future<List<Agent>> listAgents() async {
    final res = await _api.dio.get('/agents');
    return (res.data as List).map((e) => Agent.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<void> setStatus(String agentId, String status) {
    return _api.dio.patch('/agents/$agentId/status', data: {'status': status});
  }

  Future<void> reportLocation(String agentId, double lat, double lng) {
    return _api.dio.patch('/agents/$agentId/location', data: {'lat': lat, 'lng': lng});
  }
}

final dispatchRepositoryProvider = Provider((ref) => DispatchRepository(ref.watch(apiClientProvider)));

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
