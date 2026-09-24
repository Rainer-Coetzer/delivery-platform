// A single Dio client for every REST call the app makes, all through the
// gateway (see services/gateway). Attaches the stored JWT to every request
// once the user is logged in.
import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Gateway base URL. 10.0.2.2 is how the Android emulator reaches the host
/// machine's localhost; override at build time with
/// `--dart-define=API_BASE_URL=http://<lan-ip>:8080` for a real device or
/// `flutter run -d chrome` (which can use localhost directly).
const _defaultBaseUrl = String.fromEnvironment(
  'API_BASE_URL',
  defaultValue: 'http://10.0.2.2:8080',
);

class ApiClient {
  final Dio dio;
  final FlutterSecureStorage _storage;
  static const _tokenKey = 'auth_token';

  ApiClient({String? baseUrl, FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage(),
        dio = Dio(BaseOptions(
          baseUrl: baseUrl ?? _defaultBaseUrl,
          connectTimeout: const Duration(seconds: 8),
          receiveTimeout: const Duration(seconds: 8),
        )) {
    dio.interceptors.add(InterceptorsWrapper(onRequest: (options, handler) async {
      final token = await _storage.read(key: _tokenKey);
      if (token != null) {
        options.headers['Authorization'] = 'Bearer $token';
      }
      handler.next(options);
    }));
  }

  Future<void> saveToken(String token) => _storage.write(key: _tokenKey, value: token);
  Future<String?> readToken() => _storage.read(key: _tokenKey);
  Future<void> clearToken() => _storage.delete(key: _tokenKey);

  /// WebSocket URL for live order tracking, derived from the same base URL.
  String wsUrlForOrder(String orderId, String token) {
    final base = dio.options.baseUrl.replaceFirst('http', 'ws');
    return '$base/ws?orderId=${Uri.encodeQueryComponent(orderId)}&token=${Uri.encodeQueryComponent(token)}';
  }
}
