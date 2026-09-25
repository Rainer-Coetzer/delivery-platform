// The logged-in user. Core, not a module: every module needs to know who
// is signed in and to sign them out.
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'api_client.dart';
import 'models.dart';
import 'providers.dart';

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
        'name': name,
        'email': email,
        'password': password,
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
