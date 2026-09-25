import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../providers/providers.dart';

/// One merchant's catalogue ("menu" for food), with add/remove to cart.
class CatalogueScreen extends ConsumerWidget {
  final String merchantId;
  final String merchantName;
  const CatalogueScreen({super.key, required this.merchantId, required this.merchantName});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final config = ref.watch(configProvider).requireValue;
    final catalogue = ref.watch(catalogueProvider(merchantId));
    final cart = ref.watch(cartProvider);

    return Scaffold(
      appBar: AppBar(title: Text(merchantName.isEmpty ? config.title('catalogue') : merchantName)),
      body: catalogue.when(
        data: (items) => items.isEmpty
            ? Center(child: Text('Nothing on the ${config.term('catalogue')} yet'))
            : ListView.builder(
                padding: const EdgeInsets.all(12),
                itemCount: items.length,
                itemBuilder: (context, i) {
                  final item = items[i];
                  final qty = cart[item.id]?.quantity ?? 0;
                  return Card(
                    child: ListTile(
                      title: Text(item.name),
                      subtitle: Text('${config.money(item.priceCents)}${item.available ? '' : ' (unavailable)'}'),
                      trailing: item.available
                          ? Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                IconButton(
                                  icon: const Icon(Icons.remove_circle_outline),
                                  tooltip: 'Remove one',
                                  onPressed: qty > 0 ? () => ref.read(cartProvider.notifier).remove(item.id) : null,
                                ),
                                Text('$qty'),
                                IconButton(
                                  icon: const Icon(Icons.add_circle_outline),
                                  tooltip: 'Add one',
                                  onPressed: () => ref.read(cartProvider.notifier).add(item, merchantName: merchantName),
                                ),
                              ],
                            )
                          : null,
                    ),
                  );
                },
              ),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, st) => Center(child: Text('Could not load the ${config.term('catalogue')}: $e')),
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
