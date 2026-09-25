// The two things every module reads: the API client and the platform's
// configuration.
import 'dart:ui' show PlatformDispatcher;

import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'api_client.dart';
import 'config.dart';

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
