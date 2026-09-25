import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../core/module.dart';
import 'login_screen.dart';
import 'register_screen.dart';

/// Signing in, and signing up when the brand offers customers (identity).
class AuthModule extends AppModule {
  const AuthModule();

  @override
  String get name => 'auth';

  @override
  bool enabledFor(AppConfig config) => true;

  @override
  List<RouteBase> routes(AppConfig config) => [
        GoRoute(path: '/login', builder: (context, state) => const LoginScreen()),
        if (config.openSignUp) GoRoute(path: '/register', builder: (context, state) => const RegisterScreen()),
      ];
}
