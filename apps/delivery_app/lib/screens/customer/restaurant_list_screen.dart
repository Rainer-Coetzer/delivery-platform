import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../providers/providers.dart';

class RestaurantListScreen extends ConsumerWidget {
  const RestaurantListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final restaurants = ref.watch(restaurantsListProvider);
    final cart = ref.watch(cartProvider);

    return Scaffold(
      appBar: AppBar(
        title: const Text('Restaurants'),
        actions: [
          IconButton(icon: const Icon(Icons.history), onPressed: () => context.push('/orders/history')),
          IconButton(
            icon: Badge(
              label: Text('${cart.length}'),
              isLabelVisible: cart.isNotEmpty,
              child: const Icon(Icons.shopping_cart),
            ),
            onPressed: () => context.push('/cart'),
          ),
          IconButton(
            icon: const Icon(Icons.logout),
            onPressed: () => ref.read(authControllerProvider.notifier).logout(),
          ),
        ],
      ),
      body: restaurants.when(
        data: (list) => list.isEmpty
            ? const Center(child: Text('No restaurants yet — run infra/seed/seed.sh'))
            : ListView.separated(
                padding: const EdgeInsets.all(12),
                itemCount: list.length,
                separatorBuilder: (_, __) => const SizedBox(height: 8),
                itemBuilder: (context, i) {
                  final r = list[i];
                  return Card(
                    child: ListTile(
                      leading: CircleAvatar(child: Text(r.name.isNotEmpty ? r.name[0] : '?')),
                      title: Text(r.name),
                      subtitle: Text(r.isOpen ? 'Open' : 'Closed'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () => context.push('/restaurants/${r.id}/menu?name=${Uri.encodeComponent(r.name)}'),
                    ),
                  );
                },
              ),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, st) => Center(child: Text('Could not load restaurants: $e')),
      ),
    );
  }
}
