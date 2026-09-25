import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'providers/providers.dart';
import 'screens/auth/login_screen.dart';
import 'screens/auth/register_screen.dart';
import 'screens/customer/restaurant_list_screen.dart';
import 'screens/customer/menu_screen.dart';
import 'screens/customer/cart_screen.dart';
import 'screens/customer/order_tracking_screen.dart';
import 'screens/customer/order_history_screen.dart';
import 'screens/driver/driver_home_screen.dart';
import 'screens/restaurant/restaurant_board_screen.dart';
import 'screens/admin/admin_dashboard_screen.dart';

void main() {
  runApp(const ProviderScope(child: DeliveryApp()));
}

class DeliveryApp extends ConsumerWidget {
  const DeliveryApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Rebuilding GoRouter on every auth change is a simplification: it only
    // happens at login/logout, which is rare enough that resetting the
    // navigation stack at that moment is the desired behavior anyway (the
    // user should land back on their role's home screen).
    final auth = ref.watch(authControllerProvider);
    final router = _buildRouter(auth);

    return MaterialApp.router(
      title: 'Delivery Platform',
      theme: ThemeData(colorSchemeSeed: Colors.deepOrange, useMaterial3: true),
      routerConfig: router,
    );
  }

  GoRouter _buildRouter(AuthState auth) {
    return GoRouter(
      initialLocation: '/login',
      redirect: (context, state) {
        final loggedIn = auth.isLoggedIn;
        final loggingIn = state.matchedLocation == '/login' || state.matchedLocation == '/register';
        if (!loggedIn && !loggingIn) return '/login';
        if (loggedIn && loggingIn) return _homeFor(auth.user!.role);
        return null;
      },
      routes: [
        GoRoute(path: '/login', builder: (context, state) => const LoginScreen()),
        GoRoute(path: '/register', builder: (context, state) => const RegisterScreen()),
        GoRoute(path: '/restaurants', builder: (context, state) => const RestaurantListScreen()),
        GoRoute(
          path: '/restaurants/:id/menu',
          builder: (context, state) => MenuScreen(
            restaurantId: state.pathParameters['id']!,
            restaurantName: state.uri.queryParameters['name'] ?? '',
          ),
        ),
        GoRoute(path: '/cart', builder: (context, state) => const CartScreen()),
        GoRoute(
          path: '/orders/:id/track',
          builder: (context, state) => OrderTrackingScreen(orderId: state.pathParameters['id']!),
        ),
        GoRoute(path: '/orders/history', builder: (context, state) => const OrderHistoryScreen()),
        GoRoute(path: '/driver', builder: (context, state) => const DriverHomeScreen()),
        GoRoute(path: '/restaurant-board', builder: (context, state) => const RestaurantBoardScreen()),
        GoRoute(path: '/admin', builder: (context, state) => const AdminDashboardScreen()),
      ],
    );
  }

  String _homeFor(String role) {
    switch (role) {
      case 'agent':
        return '/driver';
      case 'merchant':
        return '/restaurant-board';
      case 'admin':
        return '/admin';
      default:
        return '/restaurants';
    }
  }
}
