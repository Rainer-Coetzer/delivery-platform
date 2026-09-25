import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../core/module.dart';
import 'admin_dashboard_screen.dart';

/// The admin overview (reporting). Only when the brand offers the admin role.
class AdminModule extends AppModule {
  const AdminModule();

  @override
  String get name => 'admin';

  @override
  bool enabledFor(AppConfig config) => config.roles.contains('admin');

  @override
  List<RouteBase> routes(AppConfig config) => [
        GoRoute(path: '/admin', builder: (context, state) => const AdminDashboardScreen()),
      ];

  @override
  String? homeFor(String role, AppConfig config) => role == 'admin' ? '/admin' : null;
}
