import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../core/module.dart';
import 'merchant_board_screen.dart';

/// A merchant's board: their job queue, plus tabs other modules add. Only when the brand offers the merchant role.
class MerchantModule extends AppModule {
  const MerchantModule();

  @override
  String get name => 'merchant';

  @override
  bool enabledFor(AppConfig config) => config.roles.contains('merchant');

  @override
  List<RouteBase> routes(AppConfig config) => [
        GoRoute(path: '/merchant-board', builder: (context, state) => const MerchantBoardScreen()),
      ];

  @override
  String? homeFor(String role, AppConfig config) => role == 'merchant' ? '/merchant-board' : null;
}
