import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers.dart';
import '../../l10n/app_localizations.dart';
import 'catalogue_api.dart';
import 'models.dart';

/// A merchant editing their catalogue: add items, switch them on and off.
/// Shown as a tab on the merchant board (CatalogueModule.merchantTabs).
class CatalogueEditor extends ConsumerStatefulWidget {
  final String merchantId;
  const CatalogueEditor({super.key, required this.merchantId});

  @override
  ConsumerState<CatalogueEditor> createState() => _CatalogueEditorState();
}

class _CatalogueEditorState extends ConsumerState<CatalogueEditor> {
  final _name = TextEditingController();
  final _price = TextEditingController();
  final _stock = TextEditingController(text: '10');

  @override
  Widget build(BuildContext context) {
    final catalogue = ref.watch(catalogueProvider(widget.merchantId));
    final config = ref.watch(configProvider).requireValue;
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
                      await ref.read(catalogueRepositoryProvider).updateItem(
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
    final currency = ref.read(configProvider).requireValue.brand.currency;
    final priceCents = currency.parse(_price.text);
    final stockQty = int.tryParse(_stock.text) ?? 0;
    if (priceCents == null || priceCents <= 0) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(AppLocalizations.of(context).priceExample(currency.example))));
      return;
    }
    if (_name.text.trim().isEmpty) return;
    await ref.read(catalogueRepositoryProvider).createItem(
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
