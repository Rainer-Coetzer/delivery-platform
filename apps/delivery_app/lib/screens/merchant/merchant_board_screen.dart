import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/config.dart';
import '../../models/models.dart';
import '../../providers/providers.dart';
import '../../l10n/app_localizations.dart';

/// A merchant's own board: the live job queue (mark a job ready once it's
/// prepared; accept or reject already happen automatically in the merchant
/// module, on stock and opening hours) and catalogue management. Words come
/// from the pack: "Orders" and "Menu" for a restaurant.
class MerchantBoardScreen extends ConsumerStatefulWidget {
  const MerchantBoardScreen({super.key});

  @override
  ConsumerState<MerchantBoardScreen> createState() => _MerchantBoardScreenState();
}

class _MerchantBoardScreenState extends ConsumerState<MerchantBoardScreen> with SingleTickerProviderStateMixin {
  late final TabController _tabs = TabController(length: 2, vsync: this);

  @override
  Widget build(BuildContext context) {
    final user = ref.watch(authControllerProvider).user!;
    final config = ref.watch(configProvider).requireValue;
    final merchantId = user.refId;
    final l10n = AppLocalizations.of(context);

    if (merchantId.isEmpty) {
      return Scaffold(body: Center(child: Text(l10n.noMerchantProfile(config.term('merchant')))));
    }

    return Scaffold(
      appBar: AppBar(
        title: Text(config.title('merchant')),
        bottom: TabBar(controller: _tabs, tabs: [Tab(text: config.title('jobs')), Tab(text: config.title('catalogue'))]),
        actions: [
          IconButton(icon: const Icon(Icons.logout), tooltip: l10n.logOut, onPressed: () => ref.read(authControllerProvider.notifier).logout()),
        ],
      ),
      body: TabBarView(
        controller: _tabs,
        children: [
          _JobQueue(merchantId: merchantId, config: config),
          _CatalogueEditor(merchantId: merchantId, config: config),
        ],
      ),
    );
  }
}

class _JobQueue extends ConsumerWidget {
  final String merchantId;
  final AppConfig config;
  const _JobQueue({required this.merchantId, required this.config});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final jobsAsync = ref.watch(merchantJobsProvider(merchantId));
    final l10n = AppLocalizations.of(context);
    return jobsAsync.when(
      data: (all) {
        final jobs = all.where((j) => !j.finished).toList();
        if (jobs.isEmpty) {
          return RefreshIndicator(
            onRefresh: () async => ref.invalidate(merchantJobsProvider(merchantId)),
            child: ListView(children: [Padding(padding: const EdgeInsets.all(32), child: Center(child: Text(l10n.noneActive(config.term('jobs')))))]),
          );
        }
        return RefreshIndicator(
          onRefresh: () async => ref.invalidate(merchantJobsProvider(merchantId)),
          child: ListView.builder(
            itemCount: jobs.length,
            itemBuilder: (context, i) {
              final j = jobs[i];
              return Card(
                margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                child: ListTile(
                  title: Text(l10n.jobNumberState(config.title('job'), j.id.substring(0, 8), config.stateLabel(j.status))),
                  subtitle: Text(j.items.map((it) => '${it.quantity}x ${it.name}').join(', ')),
                  // Once confirmed (paid and accepted) the merchant prepares
                  // it and says when it's ready; the milestone says when,
                  // whatever the pack calls the state.
                  trailing: j.milestone == 'confirmed'
                      ? FilledButton(
                          onPressed: () async {
                            await ref.read(merchantRepositoryProvider).markReady(merchantId, j.id);
                            ref.invalidate(merchantJobsProvider(merchantId));
                          },
                          child: Text(l10n.markReady),
                        )
                      : null,
                ),
              );
            },
          ),
        );
      },
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, st) => Center(child: Text(l10n.couldNotLoad(config.term('jobs'), '$e'))),
    );
  }
}

class _CatalogueEditor extends ConsumerStatefulWidget {
  final String merchantId;
  final AppConfig config;
  const _CatalogueEditor({required this.merchantId, required this.config});

  @override
  ConsumerState<_CatalogueEditor> createState() => _CatalogueEditorState();
}

class _CatalogueEditorState extends ConsumerState<_CatalogueEditor> {
  final _name = TextEditingController();
  final _price = TextEditingController();
  final _stock = TextEditingController(text: '10');

  @override
  Widget build(BuildContext context) {
    final catalogue = ref.watch(catalogueProvider(widget.merchantId));
    final config = widget.config;
    final l10n = AppLocalizations.of(context);
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            children: [
              Expanded(child: TextField(controller: _name, decoration: InputDecoration(labelText: l10n.itemName(config.title('catalogue_item'))))),
              const SizedBox(width: 8),
              SizedBox(
                width: 100,
                child: TextField(
                  controller: _price,
                  keyboardType: const TextInputType.numberWithOptions(decimal: true),
                  decoration: InputDecoration(labelText: l10n.price, prefixText: config.brand.currency.symbol, hintText: config.brand.currency.example),
                ),
              ),
              const SizedBox(width: 8),
              SizedBox(
                width: 80,
                child: TextField(controller: _stock, keyboardType: TextInputType.number, decoration: InputDecoration(labelText: l10n.stock)),
              ),
              IconButton(icon: const Icon(Icons.add), tooltip: l10n.add, onPressed: _addItem),
            ],
          ),
        ),
        Expanded(
          child: catalogue.when(
            data: (items) => ListView.builder(
              itemCount: items.length,
              itemBuilder: (context, i) {
                final item = items[i];
                return ListTile(
                  title: Text(item.name),
                  subtitle: Text(l10n.priceAndStock(config.money(item.priceCents), item.stockQty)),
                  trailing: Switch(
                    value: item.available,
                    onChanged: (v) async {
                      await ref.read(merchantRepositoryProvider).updateItem(
                            widget.merchantId,
                            CatalogueItem(
                                id: item.id, merchantId: item.merchantId, name: item.name, priceCents: item.priceCents, stockQty: item.stockQty, available: v),
                          );
                      ref.invalidate(catalogueProvider(widget.merchantId));
                    },
                  ),
                );
              },
            ),
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, st) => Center(child: Text(l10n.couldNotLoadThe(config.term('catalogue'), '$e'))),
          ),
        ),
      ],
    );
  }

  Future<void> _addItem() async {
    final currency = widget.config.brand.currency;
    final priceCents = currency.parse(_price.text);
    final stockQty = int.tryParse(_stock.text) ?? 0;
    if (priceCents == null || priceCents <= 0) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(AppLocalizations.of(context).priceExample(currency.example))));
      return;
    }
    if (_name.text.trim().isEmpty) return;
    await ref.read(merchantRepositoryProvider).createItem(
          widget.merchantId,
          name: _name.text.trim(),
          priceCents: priceCents,
          stockQty: stockQty,
        );
    _name.clear();
    _price.clear();
    ref.invalidate(catalogueProvider(widget.merchantId));
  }
}
