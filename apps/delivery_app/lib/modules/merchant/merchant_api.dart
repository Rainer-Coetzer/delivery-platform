// The merchant module's side of the API: the merchant's job queue and
// saying a job is ready.
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/api_client.dart';
import '../../core/jobs.dart';
import '../../core/models.dart';
import '../../core/providers.dart';

class MerchantRepository {
  final ApiClient _api;
  MerchantRepository(this._api);

  Future<void> markReady(String merchantId, String jobId) {
    return _api.dio.post('/merchants/$merchantId/jobs/$jobId/ready');
  }
}

final merchantRepositoryProvider = Provider((ref) => MerchantRepository(ref.watch(apiClientProvider)));

final merchantJobsProvider = FutureProvider.family<List<Job>, String>((ref, merchantId) {
  return ref.watch(jobRepositoryProvider).byMerchant(merchantId);
});
