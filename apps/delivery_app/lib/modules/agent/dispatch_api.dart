// The agent module's side of the API: going on or offline and reporting
// where the agent is.
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/api_client.dart';
import '../../core/providers.dart';

class DispatchRepository {
  final ApiClient _api;
  DispatchRepository(this._api);

  Future<void> setStatus(String agentId, String status) {
    return _api.dio.patch('/agents/$agentId/status', data: {'status': status});
  }

  Future<void> reportLocation(String agentId, double lat, double lng) {
    return _api.dio.patch('/agents/$agentId/location', data: {'lat': lat, 'lng': lng});
  }
}

final dispatchRepositoryProvider = Provider((ref) => DispatchRepository(ref.watch(apiClientProvider)));
