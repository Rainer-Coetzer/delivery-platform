import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'core/brand.dart';
import 'core/config.dart';
import 'providers/providers.dart';
import 'screens/admin/admin_dashboard_screen.dart';
import 'screens/agent/agent_home_screen.dart';
import 'screens/auth/login_screen.dart';
import 'screens/auth/register_screen.dart';
import 'screens/customer/cart_screen.dart';
import 'screens/customer/catalogue_screen.dart';
import 'screens/customer/job_history_screen.dart';
import 'screens/customer/job_tracking_screen.dart';
import 'screens/customer/merchant_list_screen.dart';
import 'screens/merchant/merchant_board_screen.dart';

void main() {
  runApp(const ProviderScope(child: DeliveryApp()));
}

class DeliveryApp extends ConsumerWidget {
  const DeliveryApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Every screen speaks the pack's words in the brand's colours, so
    // nothing is shown until the platform config (GET /config) is in. The
    // spinner and the error before that use Material's neutral theme:
    // there is no brand yet to use.
    final theme = ThemeData(useMaterial3: true);
    return ref.watch(configProvider).when(
          data: (config) {
            // Rebuilding GoRouter on every auth change is a simplification:
            // it only happens at login/logout, which is rare enough that
            // resetting the navigation stack then is what we want anyway
            // (the user lands on their role's home screen).
            final auth = ref.watch(authControllerProvider);
            return MaterialApp.router(
              title: config.brand.name,
              theme: buildTheme(config.brand),
              // No animation from the neutral loading theme to the brand's:
              // it would flash Material purple into the brand's colours on
              // every start.
              themeAnimationDuration: Duration.zero,
              routerConfig: _buildRouter(auth, config),
            );
          },
          loading: () => MaterialApp(theme: theme, home: const Scaffold(body: Center(child: CircularProgressIndicator()))),
          error: (e, _) => MaterialApp(
            theme: theme,
            home: Scaffold(
              body: Center(
                child: Padding(
                  padding: const EdgeInsets.all(24),
                  child: Column(mainAxisSize: MainAxisSize.min, children: [
                    Text("Can't reach the platform: $e", textAlign: TextAlign.center),
                    const SizedBox(height: 12),
                    FilledButton(onPressed: () => ref.invalidate(configProvider), child: const Text('Try again')),
                  ]),
                ),
              ),
            ),
          ),
        );
  }

  GoRouter _buildRouter(AuthState auth, AppConfig config) {
    return GoRouter(
      initialLocation: '/login',
      redirect: (context, state) {
        final loggedIn = auth.isLoggedIn;
        final loggingIn = state.matchedLocation == '/login' || state.matchedLocation == '/register';
        if (!loggedIn && !loggingIn) return '/login';
        if (loggedIn && loggingIn) return _homeFor(auth.user!.role, config);
        return null;
      },
      routes: [
        GoRoute(path: '/login', builder: (context, state) => const LoginScreen()),
        GoRoute(path: '/register', builder: (context, state) => const RegisterScreen()),
        GoRoute(path: '/merchants', builder: (context, state) => const MerchantListScreen()),
        GoRoute(
          path: '/merchants/:id/catalogue',
          builder: (context, state) => CatalogueScreen(
            merchantId: state.pathParameters['id']!,
            merchantName: state.uri.queryParameters['name'] ?? '',
          ),
        ),
        GoRoute(path: '/cart', builder: (context, state) => const CartScreen()),
        GoRoute(
          path: '/jobs/:id/track',
          builder: (context, state) => JobTrackingScreen(jobId: state.pathParameters['id']!),
        ),
        GoRoute(path: '/jobs', builder: (context, state) => const JobHistoryScreen()),
        GoRoute(path: '/agent', builder: (context, state) => const AgentHomeScreen()),
        GoRoute(path: '/merchant-board', builder: (context, state) => const MerchantBoardScreen()),
        GoRoute(path: '/admin', builder: (context, state) => const AdminDashboardScreen()),
      ],
    );
  }

  /// Each role's home. A customer browses merchants when the pack has a
  /// catalogue; otherwise they start from their own jobs (booking a
  /// courier or freight job in the app comes with the customer app, Phase E).
  String _homeFor(String role, AppConfig config) {
    switch (role) {
      case 'agent':
        return '/agent';
      case 'merchant':
        return '/merchant-board';
      case 'admin':
        return '/admin';
      default:
        return config.has('catalogue') ? '/merchants' : '/jobs';
    }
  }
}
