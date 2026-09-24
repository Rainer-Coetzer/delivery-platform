import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../models/models.dart';
import '../../providers/providers.dart';

class OrderHistoryScreen extends ConsumerWidget {
  const OrderHistoryScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final user = ref.watch(authControllerProvider).user!;

    return Scaffold(
      appBar: AppBar(title: const Text('Order history')),
      body: FutureBuilder<List<Order>>(
        future: ref.read(orderRepositoryProvider).byCustomer(user.id),
        builder: (context, snapshot) {
          if (!snapshot.hasData) return const Center(child: CircularProgressIndicator());
          final orders = snapshot.data!;
          if (orders.isEmpty) return const Center(child: Text('No orders yet'));
          return ListView.builder(
            itemCount: orders.length,
            itemBuilder: (context, i) {
              final o = orders[i];
              return ListTile(
                title: Text(o.restaurantName),
                subtitle: Text('N\$${(o.totalCents / 100).toStringAsFixed(2)} — ${o.status}'),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => context.push('/orders/${o.id}/track'),
              );
            },
          );
        },
      ),
    );
  }
}
