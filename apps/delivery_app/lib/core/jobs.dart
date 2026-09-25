// Jobs are the core's noun (ADR-0003): reading them and following one live
// is shared by every module. Creating one is a module's business (the
// catalogue module places a job from a cart).
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'api_client.dart';
import 'models.dart';
import 'providers.dart';

class JobRepository {
  final ApiClient _api;
  JobRepository(this._api);

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

/// The signed-in customer's jobs, newest first. A provider rather than a
/// future made in build(), so a rebuild doesn't fetch again.
final customerJobsProvider = FutureProvider.autoDispose.family<List<Job>, String>((ref, customerId) {
  return ref.watch(jobRepositoryProvider).byCustomer(customerId);
});
