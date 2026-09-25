// The catalogue module's side of the API: merchants and their catalogues,
// the customer's cart, and placing a job from it.
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/api_client.dart';
import '../../core/models.dart';
import '../../core/providers.dart';
import 'models.dart';

class CatalogueRepository {
  final ApiClient _api;
  CatalogueRepository(this._api);

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
      'name': name,
      'priceCents': priceCents,
      'stockQty': stockQty,
      'available': true,
    });
  }

  /// Sends only what the customer chooses. The server prices every line
  /// from the merchant's own catalogue, takes the customer from the token
  /// and the pickup point from the merchant, so none of that is sent.
  Future<Job> placeJob({
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

  Future<void> updateItem(String merchantId, CatalogueItem item) {
    return _api.dio.patch('/merchants/$merchantId/catalogue-items/${item.id}', data: {
      'name': item.name,
      'priceCents': item.priceCents,
      'stockQty': item.stockQty,
      'available': item.available,
    });
  }
}

final catalogueRepositoryProvider = Provider((ref) => CatalogueRepository(ref.watch(apiClientProvider)));

final merchantsListProvider = FutureProvider<List<Merchant>>((ref) {
  return ref.watch(catalogueRepositoryProvider).list();
});

final catalogueProvider = FutureProvider.family<List<CatalogueItem>, String>((ref, merchantId) {
  return ref.watch(catalogueRepositoryProvider).catalogue(merchantId);
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
      item.id: existing != null ? existing.withQuantity(existing.quantity + 1) : CartLine(item: item),
    };
  }

  void remove(String itemId) {
    final next = {...state};
    final line = next[itemId];
    if (line == null) return;
    if (line.quantity <= 1) {
      next.remove(itemId);
    } else {
      next[itemId] = line.withQuantity(line.quantity - 1);
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
