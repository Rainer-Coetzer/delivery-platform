import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../models/models.dart';
import '../../providers/providers.dart';

class MenuScreen extends ConsumerWidget {
  final String restaurantId;
  final String restaurantName;
  const MenuScreen({super.key, required this.restaurantId, required this.restaurantName});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final menu = ref.watch(menuProvider(restaurantId));
    final cart = ref.watch(cartProvider);

    return Scaffold(
      appBar: AppBar(title: Text(restaurantName.isEmpty ? 'Menu' : restaurantName)),
      body: menu.when(
        data: (items) => items.isEmpty
            ? const Center(child: Text('No menu items yet'))
            : ListView.builder(
                padding: const EdgeInsets.all(12),
                itemCount: items.length,
                itemBuilder: (context, i) {
                  final item = items[i];
                  final qty = cart[item.id]?.quantity ?? 0;
                  return Card(
                    child: ListTile(
                      title: Text(item.name),
                      subtitle: Text('N\$${(item.priceCents / 100).toStringAsFixed(2)}'
                          '${item.available ? '' : ' — unavailable'}'),
                      trailing: item.available
                          ? Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                IconButton(
                                  icon: const Icon(Icons.remove_circle_outline),
                                  onPressed: qty > 0 ? () => ref.read(cartProvider.notifier).remove(item.id) : null,
                                ),
                                Text('$qty'),
                                IconButton(
                                  icon: const Icon(Icons.add_circle_outline),
                                  onPressed: () => ref.read(cartProvider.notifier).add(item, restaurantName: restaurantName),
                                ),
                              ],
                            )
                          : null,
                    ),
                  );
                },
              ),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, st) => Center(child: Text('Could not load menu: $e')),
      ),
      floatingActionButton: cart.isNotEmpty
          ? FloatingActionButton.extended(
              onPressed: () => context.push('/cart'),
              icon: const Icon(Icons.shopping_cart),
              label: Text('View cart (${cart.length})'),
            )
          : null,
    );
  }
}
