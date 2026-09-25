// The admin module's side of the API: the reporting overview.
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/api_client.dart';
import '../../core/providers.dart';

class AdminRepository {
  final ApiClient _api;
  AdminRepository(this._api);

  Future<Map<String, dynamic>> overview() async {
    final res = await _api.dio.get('/admin/overview');
    return res.data as Map<String, dynamic>;
  }
}

final adminRepositoryProvider = Provider((ref) => AdminRepository(ref.watch(apiClientProvider)));

/// A provider rather than a future made in build(), so a rebuild doesn't
/// fetch again. Pull to refresh invalidates it.
final adminOverviewProvider = FutureProvider.autoDispose<Map<String, dynamic>>((ref) {
  return ref.watch(adminRepositoryProvider).overview();
});
