import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/models.dart';
import '../../providers/providers.dart';

/// A restaurant's own dashboard: the live order queue (mark ready once the
/// kitchen has finished — accept/reject already happen automatically in
/// services/restaurant based on stock and hours) and menu management.
class RestaurantBoardScreen extends ConsumerStatefulWidget {
  const RestaurantBoardScreen({super.key});

  @override
  ConsumerState<RestaurantBoardScreen> createState() => _RestaurantBoardScreenState();
}

class _RestaurantBoardScreenState extends ConsumerState<RestaurantBoardScreen> with SingleTickerProviderStateMixin {
  late final TabController _tabs = TabController(length: 2, vsync: this);

  @override
  Widget build(BuildContext context) {
    final user = ref.watch(authControllerProvider).user!;
    final restaurantId = user.refId;

    if (restaurantId.isEmpty) {
      return const Scaffold(body: Center(child: Text('No restaurant profile linked to this account yet.')));
    }

    return Scaffold(
      appBar: AppBar(
        title: const Text('Restaurant'),
        bottom: TabBar(controller: _tabs, tabs: const [Tab(text: 'Orders'), Tab(text: 'Menu')]),
        actions: [
          IconButton(icon: const Icon(Icons.logout), onPressed: () => ref.read(authControllerProvider.notifier).logout()),
        ],
      ),
      body: TabBarView(
        controller: _tabs,
        children: [
          _OrderQueue(restaurantId: restaurantId),
          _MenuEditor(restaurantId: restaurantId),
        ],
      ),
    );
  }
}

class _OrderQueue extends ConsumerWidget {
  final String restaurantId;
  const _OrderQueue({required this.restaurantId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ordersAsync = ref.watch(restaurantOrdersProvider(restaurantId));
    return ordersAsync.when(
      data: (all) {
        final orders = all.where((o) => o.status != 'DELIVERED' && o.status != 'CANCELLED').toList();
        if (orders.isEmpty) {
          return RefreshIndicator(
            onRefresh: () async => ref.invalidate(restaurantOrdersProvider(restaurantId)),
            child: ListView(children: const [Padding(padding: EdgeInsets.all(32), child: Center(child: Text('No active orders')))]),
          );
        }
        return RefreshIndicator(
          onRefresh: () async => ref.invalidate(restaurantOrdersProvider(restaurantId)),
          child: ListView.builder(
            itemCount: orders.length,
            itemBuilder: (context, i) {
              final o = orders[i];
              return Card(
                margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                child: ListTile(
                  title: Text('Order #${o.id.substring(0, 8)} — ${o.status}'),
                  subtitle: Text(o.items.map((it) => '${it.quantity}x ${it.name}').join(', ')),
                  trailing: o.status == 'PREPARING'
                      ? FilledButton(
                          onPressed: () async {
                            await ref.read(restaurantRepositoryProvider).markReady(restaurantId, o.id);
                            ref.invalidate(restaurantOrdersProvider(restaurantId));
                          },
                          child: const Text('Mark ready'),
                        )
                      : null,
                ),
              );
            },
          ),
        );
      },
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, st) => Center(child: Text('Could not load orders: $e')),
    );
  }
}

class _MenuEditor extends ConsumerStatefulWidget {
  final String restaurantId;
  const _MenuEditor({required this.restaurantId});

  @override
  ConsumerState<_MenuEditor> createState() => _MenuEditorState();
}

class _MenuEditorState extends ConsumerState<_MenuEditor> {
  final _name = TextEditingController();
  final _price = TextEditingController();
  final _stock = TextEditingController(text: '10');

  @override
  Widget build(BuildContext context) {
    final menu = ref.watch(menuProvider(widget.restaurantId));
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            children: [
              Expanded(child: TextField(controller: _name, decoration: const InputDecoration(labelText: 'Item name'))),
              const SizedBox(width: 8),
              SizedBox(
                width: 100,
                child: TextField(controller: _price, keyboardType: TextInputType.number, decoration: const InputDecoration(labelText: 'Price (N\$)')),
              ),
              const SizedBox(width: 8),
              SizedBox(
                width: 80,
                child: TextField(controller: _stock, keyboardType: TextInputType.number, decoration: const InputDecoration(labelText: 'Stock')),
              ),
              IconButton(icon: const Icon(Icons.add), onPressed: _addItem),
            ],
          ),
        ),
        Expanded(
          child: menu.when(
            data: (items) => ListView.builder(
              itemCount: items.length,
              itemBuilder: (context, i) {
                final item = items[i];
                return ListTile(
                  title: Text(item.name),
                  subtitle: Text('N\$${(item.priceCents / 100).toStringAsFixed(2)} — stock ${item.stockQty}'),
                  trailing: Switch(
                    value: item.available,
                    onChanged: (v) async {
                      await ref.read(restaurantRepositoryProvider).updateMenuItem(
                            widget.restaurantId,
                            MenuItem(id: item.id, restaurantId: item.restaurantId, name: item.name, priceCents: item.priceCents, stockQty: item.stockQty, available: v),
                          );
                      ref.invalidate(menuProvider(widget.restaurantId));
                    },
                  ),
                );
              },
            ),
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, st) => Center(child: Text('Could not load menu: $e')),
          ),
        ),
      ],
    );
  }

  Future<void> _addItem() async {
    final priceCents = ((double.tryParse(_price.text) ?? 0) * 100).round();
    final stockQty = int.tryParse(_stock.text) ?? 0;
    if (_name.text.trim().isEmpty || priceCents <= 0) return;
    await ref.read(restaurantRepositoryProvider).createMenuItem(
          widget.restaurantId,
          name: _name.text.trim(),
          priceCents: priceCents,
          stockQty: stockQty,
        );
    _name.clear();
    _price.clear();
    ref.invalidate(menuProvider(widget.restaurantId));
  }
}
