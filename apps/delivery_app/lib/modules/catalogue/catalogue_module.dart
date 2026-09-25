import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../core/module.dart';
import 'cart_screen.dart';
import 'catalogue_editor.dart';
import 'catalogue_screen.dart';
import 'merchant_list_screen.dart';

/// Merchants with catalogues: customers browse and order from them, and a
/// merchant edits theirs on the board. Only when the pack enables the
/// catalogue module (food does; courier and freight don't).
class CatalogueModule extends AppModule {
  const CatalogueModule();

  @override
  String get name => 'catalogue';

  @override
  bool enabledFor(AppConfig config) => config.has('catalogue');

  @override
  List<RouteBase> routes(AppConfig config) => [
        GoRoute(path: '/merchants', builder: (context, state) => const MerchantListScreen()),
        GoRoute(
          path: '/merchants/:id/catalogue',
          builder: (context, state) => CatalogueScreen(
            merchantId: state.pathParameters['id']!,
            merchantName: state.uri.queryParameters['name'] ?? '',
          ),
        ),
        GoRoute(path: '/cart', builder: (context, state) => const CartScreen()),
      ];

  @override
  String? homeFor(String role, AppConfig config) => role == 'customer' ? '/merchants' : null;

  @override
  List<MerchantTab> merchantTabs(AppConfig config) => [
        MerchantTab(label: config.title('catalogue'), build: (merchantId) => CatalogueEditor(merchantId: merchantId)),
      ];
}
