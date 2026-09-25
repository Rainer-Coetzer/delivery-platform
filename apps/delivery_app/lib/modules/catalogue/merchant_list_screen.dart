import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/providers.dart';
import '../../core/session.dart';
import '../../l10n/app_localizations.dart';
import 'catalogue_api.dart';

/// The merchants a customer can order from (catalogue module). Words come
/// from the pack: "Restaurants" for food.
class MerchantListScreen extends ConsumerWidget {
  const MerchantListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final config = ref.watch(configProvider).requireValue;
    final merchants = ref.watch(merchantsListProvider);
    final cart = ref.watch(cartProvider);
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(
        title: Text(config.title('merchants')),
        actions: [
          IconButton(icon: const Icon(Icons.history), tooltip: l10n.yourJobs(config.term('jobs')), onPressed: () => context.push('/jobs')),
          IconButton(
            icon: Badge(
              label: Text('${cart.length}'),
              isLabelVisible: cart.isNotEmpty,
              child: const Icon(Icons.shopping_cart),
            ),
            tooltip: l10n.cart,
            onPressed: () => context.push('/cart'),
          ),
          IconButton(
            icon: const Icon(Icons.logout),
            tooltip: l10n.logOut,
            onPressed: () => ref.read(authControllerProvider.notifier).logout(),
          ),
        ],
      ),
      body: merchants.when(
        data: (list) => list.isEmpty
            ? Center(child: Text(l10n.noneYet(config.term('merchants'))))
            : ListView.separated(
                padding: const EdgeInsets.all(12),
                itemCount: list.length,
                separatorBuilder: (_, __) => const SizedBox(height: 8),
                itemBuilder: (context, i) {
                  final m = list[i];
                  return Card(
                    child: ListTile(
                      leading: CircleAvatar(child: Text(m.name.isNotEmpty ? m.name[0] : '?')),
                      title: Text(m.name),
                      subtitle: Text(m.isOpen ? l10n.open : l10n.closed),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () => context.push('/merchants/${m.id}/catalogue?name=${Uri.encodeComponent(m.name)}'),
                    ),
                  );
                },
              ),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, st) => Center(child: Text(l10n.couldNotLoad(config.term('merchants'), '$e'))),
      ),
    );
  }
}
